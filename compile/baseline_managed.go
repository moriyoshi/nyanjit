package compile

import (
	"fmt"
	"math/bits"
	"slices"
	"strconv"
	"strings"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// Go-managed functions (ContextGoManaged) at the baseline tier. They follow
// the optimized tier's rules:
//
//   - The frame has a Go entry's layout, whatever the entry ABI: Go argument
//     homes below the spill slots (and amd64's reserved top word), and on
//     ppc64le Go's linkage without a back chain.
//   - Every outbound call is a Go call. A C call goes through the gateway
//     cCallBridgeN (cdeclCallBridgeN on 386), which takes the target and the
//     arguments and makes the runtime-aware transition to C; the Template
//     links its address. A Go call can grow the stack, which the runtime
//     copies through the registered, pointer-free frame.
//   - Go's reserved registers (amd64 R14 and R15, and g on every target)
//     keep their values: the models never allocate them.
//   - A C entry that calls Go code preserves every register the C ABI
//     preserves and Go clobbers: those the engine allocates are saved as it
//     writes them, the rest (ppc64le R29, R31, CR2-CR4 and V20-V31, riscv64
//     X8 and X9) are added to the saves. A Go entry saves none.
//   - The Template registers the frame with the Go runtime through
//     goregister, with the unwind table built from the encoded frame
//     operations, and lists the fixed CallGo targets Bind validates.

// baselineGoTargets are a Go-managed function's fixed CallGo targets, or why
// a target is not fixed.
type baselineGoTargets struct {
	targets []execution.GoCallTarget
	err     error
}

// managedModel adjusts the register model for a Go-managed function.
func (e *baselineEngine) managedModel() {
	e.managed.targets, e.managed.err = e.managed.targets[:0], nil
	m := &e.m
	// The frame is a Go entry's, as the optimized tier lays it out.
	g := e.options
	g.ABI = abi.Go
	gm := e.arch.model(g)
	m.goHomes, m.spillLimit = gm.goHomes, gm.spillLimit
	if e.options.ABI != abi.C {
		// Go's ABI preserves no register, so a Go entry saves none: the
		// frame stays within the managed-frame limit, and the report lists
		// the C-preserved registers it writes. The 386 frame's pushes stay.
		if e.target.architecture != "386" {
			m.saved = 0
		}
		return
	}
	switch e.target.architecture {
	case "amd64":
		// R14 is g and R15 Go's GOT temporary. R15 stays in saved: a Go
		// callee clobbers it.
		m.allocatable &^= regBit(14) | regBit(15)
		m.calleeSavedGP = amd64ManagedSavedC
	case "riscv64":
		// The frame pointer and S1, which the engine does not allocate.
		m.saved |= regBit(8) | regBit(9)
	case "ppc64le":
		// The workspace register and Go's assembler temporary.
		m.saved |= regBit(29) | regBit(31)
	}
}

var amd64ManagedSavedC = []int8{3, 12, 13}

// checkManaged applies Lower's checks for ContextGoManaged and records the
// function's fixed CallGo targets.
func (e *baselineEngine) checkManaged() error {
	o := &e.options
	if o.ABI != abi.Go && o.ABI != abi.C {
		return ErrUnsupportedABI
	}
	if !o.AssumePointerFree {
		return ErrPointerMapsRequired
	}
	if o.Stack != abi.StackPlatform || o.StackPointer != abi.StackPointerReserved {
		return ErrStackRequired
	}
	// C calls are Go calls to the gateways.
	e.hasGoCall = e.hasGoCall || e.hasFunctionCall
	e.managedTargets()
	return nil
}

// managedTargets records the fixed destinations of the CallGo records, as
// callableGoTargets does for the optimized tier: a native-width cast or an
// immutable Var's Get preserves a fixed target.
func (e *baselineEngine) managedTargets() {
	m := &e.managed
	for k := range e.recs {
		r := &e.recs[k]
		if r.op != ir.OpCallGo {
			continue
		}
		v := e.args[r.x]
		for steps := 0; steps < len(e.recs); steps++ {
			t := &e.recs[v]
			if t.op == ir.OpCastUnsigned && t.bits >= uint8(strconv.IntSize) {
				v = t.a
				continue
			}
			if t.op == ir.OpGet {
				if bv := &e.vars[t.x]; bv.immutable && bv.parts == 1 {
					v = bv.initial[0]
					continue
				}
			}
			break
		}
		t := &e.recs[v]
		if t.op != ir.OpConst {
			m.err = fmt.Errorf("CallGo requires a fixed Go function constant or symbol; dynamic targets are forbidden: %w", ErrInvalidFunction)
			return
		}
		target := execution.GoCallTarget{Address: t.imm}
		if t.fl&rfSymbol != 0 {
			target = execution.GoCallTarget{Symbol: e.symbols[t.imm]}
		}
		if !slices.Contains(m.targets, target) {
			m.targets = append(m.targets, target)
		}
	}
}

// managedUnit returns the CallGo targets for the unit.
func (e *baselineEngine) managedUnit() baselineGoTargets {
	return baselineGoTargets{targets: slices.Clone(e.managed.targets), err: e.managed.err}
}

// bridgeEffect returns the effect of the gateway call that replaces C call
// record r: a Go call whose arguments are the target and r's arguments.
func (e *baselineEngine) bridgeEffect(r *bRec) (abi.CallEffect, error) {
	return cachedCallEffect(abi.CallSpec{Architecture: e.target.architecture, Kind: abi.FunctionCall, ABI: abi.Go, ArgumentCount: int(r.n)})
}

// floatBridge returns the effect of the gateway call that replaces C call
// record r with float passing, a Go call whose arguments are the adapter,
// the target and r's arguments, and the index in symbols of the adapter
// (see float_adapter.go).
func (e *baselineEngine) floatBridge(r *bRec) (abi.CallEffect, int, error) {
	n := int(r.n) // the target and the arguments
	if n > 8 || n > abi.MaxArgumentsFor(e.target.architecture) {
		return abi.CallEffect{}, 0, fmt.Errorf("Go-managed C call with float passing has %d arguments, limit %d: %w", n-1, min(8, abi.MaxArgumentsFor(e.target.architecture))-1, ErrBackendLimit)
	}
	floats := e.callFloats(r)
	widths := make([]uint8, n-1)
	for k, v := range e.args[r.x+1 : r.x+uint32(r.n)] {
		widths[k] = e.valueBits(v)
	}
	e.symbols = append(e.symbols, floatAdapterSymbol(floats, floatAdapterBits(e.target.architecture, floats, widths)))
	effect, err := cachedCallEffect(abi.CallSpec{Architecture: e.target.architecture, Kind: abi.FunctionCall, ABI: abi.Go, ArgumentCount: n + 1})
	return effect, len(e.symbols) - 1, err
}

// loadBridge loads the gateway for a C call with n arguments into the call
// target register. Its address is a symbol, so that a listing describes the
// code the same way on every host; the Template links it before Bind sees
// the code, so that no binding can replace it.
func (e *baselineEngine) loadBridge(n int) error {
	reg, w, name := e.m.callTarget, uint8(64), "nyanjit.cCallBridge"+strconv.Itoa(n)
	if e.target.architecture == "386" {
		w, name = 32, "nyanjit.cdeclCallBridge"+strconv.Itoa(n)
	}
	e.alloc.used |= regBit(reg)
	e.symbols = append(e.symbols, name)
	e.put(bPhys{k: pkSymbol, w: w, a: reg, imm: uint64(len(e.symbols) - 1)})
	return nil
}

// linkBridges returns the unit's code with the gateway addresses linked,
// and the relocations that remain for Bind.
func (u *baselineUnit) linkBridges() ([]byte, []machine.Relocation, error) {
	var bridges, rest []machine.Relocation
	bindings := map[string]uint64{}
	for _, r := range u.relocs {
		if strings.HasPrefix(r.Symbol, floatAdapterPrefix) {
			address, err := floatAdapterAddress(r.Symbol)
			if err != nil {
				return nil, nil, err
			}
			bindings[r.Symbol] = uint64(address)
			bridges = append(bridges, r)
			continue
		}
		prefix := "nyanjit.cCallBridge"
		if u.architecture == "386" {
			prefix = "nyanjit.cdeclCallBridge"
		}
		suffix, ok := strings.CutPrefix(r.Symbol, prefix)
		n, err := strconv.Atoi(suffix)
		if !ok || err != nil {
			rest = append(rest, r)
			continue
		}
		address := cCallBridgeAddress(n)
		if u.architecture == "386" {
			address = cdeclCallBridgeAddress(n)
		}
		if address == 0 {
			return nil, nil, ErrBackendLimit
		}
		bindings[r.Symbol] = uint64(address)
		bridges = append(bridges, r)
	}
	if len(bridges) == 0 {
		return u.code, u.relocs, nil
	}
	code, err := machine.LinkRelocations(u.code, bridges, bindings)
	return code, rest, err
}

// i386BridgeArea is the call area of the gateway call for C call record r:
// a uint64 slot for the encoded target and each argument, and the result.
func i386BridgeArea(r *bRec) int { return 8*int(r.n) + 8 }

// i386BridgeCall lowers Go-managed C call record r, whose value is i, as a
// call to the cdecl gateway. Its first uint64 holds the target in the low
// word and, in the high word, a mask of the arguments that take two cdecl
// words; each argument is zero-extended to a uint64 slot.
func i386BridgeCall(e *baselineEngine, i uint32, r *bRec) error {
	a := &e.alloc
	if _, err := e.callEffect(r); err != nil {
		return err
	}
	effect, err := e.bridgeEffect(r)
	if err != nil {
		return err
	}
	// With float passing the gateway calls the adapter, whose first
	// argument is the target.
	adapter, shift := -1, 0
	if e.callFloats(r) != 0 {
		if effect, adapter, err = e.floatBridge(r); err != nil {
			return err
		}
		shift = 1
	}
	clobber := uint64(0)
	for _, list := range [2][]string{effect.ClobberedRegisters, effect.ClobberedFPRegisters} {
		for _, name := range list {
			if reg, ok := e.arch.register(name); ok {
				clobber |= regBit(reg)
			}
		}
	}
	a.callers |= clobber
	args := e.args[r.x : r.x+uint32(r.n)]
	liveAfter := func(v uint32) bool {
		for j, x := range args {
			if x == v {
				return a.argNu[int(r.x)+j] != noValue
			}
		}
		return a.vs[v].next != noValue
	}
	// Values live after the call leave the clobbered registers.
	for m := ^a.free & e.m.allocatable & clobber; m != 0; m &= m - 1 {
		reg := int8(bits.TrailingZeros64(m))
		v := a.regVal[reg]
		if v == noValue || !liveAfter(v) {
			continue
		}
		if t := a.free & e.m.allocatable &^ clobber &^ a.pinned & bankMask(regBank(reg)); t != 0 {
			to := int8(bits.TrailingZeros64(t))
			e.put(bPhys{k: pkMove, a: to, b: reg})
			a.regVal[reg] = noValue
			a.free |= regBit(reg)
			e.bind(v, to)
			continue
		}
		e.evict(reg)
	}
	mask := uint64(0)
	for k, v := range args[1:] {
		if e.valueBits(v) == 64 {
			mask |= 1 << (k + shift)
		}
	}
	if adapter >= 0 {
		e.put(bPhys{k: pkSymbol, w: 32, a: x86EAX, imm: uint64(adapter)})
		e.put(bPhys{k: pkOp, sub: i386ArgStore, w: 32, a: x86EAX, imm: 0})
		i386StoreBridgeWord(e, args[0], 8, false)
	} else {
		i386StoreBridgeWord(e, args[0], 0, false)
	}
	e.put(bPhys{k: pkConst, a: x86EAX, imm: mask})
	e.put(bPhys{k: pkOp, sub: i386ArgStore, w: 32, a: x86EAX, imm: 4})
	for k, v := range args[1:] {
		i386StoreBridgeWord(e, v, uint64(8*(k+1+shift)), e.valueBits(v) == 64)
	}
	if err := e.loadBridge(len(args) - 1 + shift); err != nil {
		return err
	}
	e.put(bPhys{k: pkCall, sub: uint8(ir.OpCallGo)})
	a.hasCall, a.flags = true, true
	a.used |= clobber | i386Temps | regBit(x86X0) | regBit(x86X2)
	for m := clobber & e.m.allocatable; m != 0; m &= m - 1 {
		reg := int8(bits.TrailingZeros64(m))
		if a.regVal[reg] != noValue {
			e.unbind(reg)
		}
	}
	for j, v := range args {
		dup := false
		for q := 0; q < j; q++ {
			dup = dup || args[q] == v
		}
		if !dup {
			e.advance(v, a.argNu[int(r.x)+j])
		}
	}
	if a.vs[i].next == noValue {
		return nil
	}
	e.put(bPhys{k: pkOp, sub: i386GoResult, imm: uint64(8 * (len(args) + shift))})
	if e.recs[i].bits == 64 {
		dst := e.def(i, bankFP, 0)
		e.put(bPhys{k: pkOp, sub: i386PairToX, a: dst})
	} else {
		dst := e.def(i, bankGP, regBit(x86EAX)|regBit(x86EDX))
		e.put(bPhys{k: pkMove, a: dst, b: x86EAX})
	}
	e.defined(i)
	return nil
}

// i386StoreBridgeWord stores v to the call area at off: all 64 bits when
// wide, and otherwise its low word, followed by a zero high word for an
// argument slot (off > 0).
func i386StoreBridgeWord(e *baselineEngine, v uint32, off uint64, wide bool) {
	s := &e.alloc.vs[v]
	w := uint8(32)
	if wide {
		w = 64
	}
	switch {
	case s.reg >= 0:
		e.put(bPhys{k: pkOp, sub: i386ArgStore, w: w, a: s.reg, imm: off})
	case e.isConst(v) && e.recs[v].fl&rfSymbol == 0:
		c := e.recs[v].imm
		e.put(bPhys{k: pkConst, a: x86EAX, imm: uint64(uint32(c))})
		e.put(bPhys{k: pkOp, sub: i386ArgStore, w: 32, a: x86EAX, imm: off})
		if wide {
			e.put(bPhys{k: pkConst, a: x86EAX, imm: c >> 32})
			e.put(bPhys{k: pkOp, sub: i386ArgStore, w: 32, a: x86EAX, imm: off + 4})
		}
	default:
		t := x86EAX
		if wide || e.valueBits(v) == 64 {
			t = x86X0
		}
		e.load(t, v)
		e.put(bPhys{k: pkOp, sub: i386ArgStore, w: w, a: t, imm: off})
	}
	if !wide && off != 0 {
		e.put(bPhys{k: pkConst, a: x86EAX, imm: 0})
		e.put(bPhys{k: pkOp, sub: i386ArgStore, w: 32, a: x86EAX, imm: off + 4})
	}
}

// ppc64ManagedWords is the number of frame words a Go-managed ppc64le C entry with
// calls adds at the top of its frame: CR (one word below the vectors) and
// V20-V31 (12 aligned quadwords).
const ppc64ManagedWords = 1 + 2*12

// ppc64ManagedSaves reports whether the function is a Go-managed ppc64le C
// entry with calls: Go clobbers the CR fields and vector registers the C ABI
// preserves, so its frame saves them, as the optimized tier's entry
// resources do.
func ppc64ManagedSaves(e *baselineEngine) bool {
	return e.options.ManagedCalls && e.options.ABI == abi.C && e.options.Stack == abi.StackPlatform && e.alloc.hasCall
}

// ppc64ManagedResources emits the saves (after the frame is entered) or the
// restores (before it is left) of CR2-CR4 and V20-V31 at the top of a frame
// of frame bytes. R11 is free in the prologue and the epilogue.
func ppc64ManagedResources(e *baselineEngine, o *ppcOut, frame int, save bool) {
	const spillOffset = ppc64SpillOffset
	cr := (frame - 8*ppc64ManagedWords - spillOffset) / 8
	vector := func(k int) int { return (frame - 16*(12-k) - spillOffset) / 8 }
	if save {
		o.expand(e, machine.Instruction{Op: machine.OpSaveCondition, Slot: cr})
		for k := range 12 {
			o.expand(e, machine.Instruction{Op: machine.OpSaveCVector, A: 20 + backend.Register(k), Slot: vector(k)})
		}
		return
	}
	for k := 11; k >= 0; k-- {
		o.expand(e, machine.Instruction{Op: machine.OpRestoreCVector, A: 20 + backend.Register(k), Slot: vector(k)})
	}
	o.expand(e, machine.Instruction{Op: machine.OpRestoreCondition, Slot: cr})
}

// ppc64ManagedReport adds the vector registers a Go-managed function's Go
// callees clobber, which the engine does not model: V0-V31, less V20-V31
// that a C entry saves. R2 and LR, which the calls and the frame restore,
// are not clobbered.
func ppc64ManagedReport(u *baselineUnit, r *abi.EmissionReport) {
	last := 31
	if u.options.ABI == abi.C {
		last = 19
	}
	for k := 0; k <= last; k++ {
		r.ClobberedFPRegisters = append(r.ClobberedFPRegisters, "V"+strconv.Itoa(k))
	}
	r.ClobberedRegisters = slices.DeleteFunc(r.ClobberedRegisters, func(name string) bool { return name == "R2" })
	r.ClobberedSpecialRegisters = slices.DeleteFunc(r.ClobberedSpecialRegisters, func(name string) bool { return name == "LR" })
	if len(r.ClobberedRegisters) == 0 {
		r.ClobberedRegisters = nil
	}
}

// managedTemplate builds a Go-managed unit's Template, checking the frame
// against the target's managed-frame rules as Unit.Template does.
func (u *baselineUnit) managedTemplate(view abi.Report) (*execution.Template, error) {
	target := newMachineTarget(u.options)
	if target == nil {
		return nil, ErrUnsupported
	}
	limit := target.ManagedFrameLimit()
	report := u.frameReport()
	switch {
	case limit == 0:
		return nil, fmt.Errorf("%s has no managed frame support: %w", target.Architecture(), ErrBackendLimit)
	case report.WorkspaceBytes != 0:
		return nil, fmt.Errorf("managed entry requires %d workspace bytes: %w", report.WorkspaceBytes, ErrBackendLimit)
	case report.StackBytes > limit:
		return nil, &LimitError{Kind: LimitFrameBytes, Needed: report.StackBytes, Limit: limit, Arch: target.Architecture(), Detail: "managed frame"}
	case report.StackBytes == 0 && u.facts.hasFunctionCall:
		return nil, fmt.Errorf("managed calls require a nonempty frame: %w", ErrBackendLimit)
	}
	if u.managed.err != nil {
		return nil, u.managed.err
	}
	code, relocs, err := u.linkBridges()
	if err != nil {
		return nil, err
	}
	unwind, err := u.goUnwind()
	if err != nil {
		return nil, err
	}
	emission := emissionTemplate{Bytes: code, Report: report, Relocations: relocs, goUnwind: unwind}
	return newExecutableTemplate(emission, view, u.arity, u.paramBits, u.paramTypes, u.finalizers, u.options.ABI,
		false, true, false, u.managed.targets, u.options.Harden), nil
}

// goUnwind builds the unit's Go unwind table with the host target's GoFrame,
// from the PCs of its frame operations, returns and calls.
func (u *baselineUnit) goUnwind() (goUnwindInfo, error) {
	retBytes := 4
	if u.architecture == "amd64" || u.architecture == "386" {
		retBytes = 1
	}
	program := physicalProgram{codeBytes: len(u.code), stackBytes: u.facts.frame}
	add := func(op machine.Opcode, pc int, imm uint64) {
		program.code.add(&physicalInsn{Op: op, Imm: imm}, pc)
	}
	pc := 0
	for i := range u.phys {
		p := &u.phys[i]
		sz := int(p.sz)
		switch p.k {
		case pkFrameEnter:
			add(machine.OpFrameEnter, pc, p.imm)
		case pkFrameLeave:
			at := pc
			if u.architecture == "ppc64le" && p.sub != 0 {
				// The C entry's restores precede the frame's own leave.
				at = pc + sz - ppc64EmitterFor(&u.options).Size(machine.Instruction{Op: machine.OpFrameLeave, Imm: p.imm})
			}
			add(machine.OpFrameLeave, at, p.imm)
		case pkRet:
			// A 386 Go entry stores its result before the RET.
			add(machine.OpReturn, pc+sz-retBytes, 0)
			if pc+sz < len(u.code) {
				// The code after the return runs with the frame open.
				add(machine.OpMove, pc+sz, 0)
			}
		case pkCall:
			add(machine.OpCallGo, pc, 0)
		}
		pc += sz
	}
	return goUnwindFromProgram(program)
}
