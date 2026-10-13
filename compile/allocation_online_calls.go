package compile

import (
	"slices"

	"github.com/moriyoshi/nyanjit/ir"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
)

// Calls on the regalloc.Allocator protocol. A call is a position whose
// inputs, the call target or system call number and then the arguments, are
// placed in the effect's registers, and whose result arrives in its result
// register. Online keeps the values that live past it out of the registers
// it overwrites. The walk saves the reserved homes the call overwrites and
// restores them after it, as the direct walk's beforeHomeCall and
// afterHomeCall do, so a value read in place from a home survives the call.
// It moves the inputs whose registers are outside the allocatable set, then
// writes the constant inputs, which need no definition, and those read in
// place, from their home register or, when the call's inputs take that, its
// slot.

// onlineCall is one call of the program, as planOnlineCalls reads it.
type onlineCall struct {
	position int
	kind     abi.CallKind
	effect   abi.CallEffect // the emitter's
	// fixed is each input's register, named names. The inputs in outside, a
	// mask, have one outside the allocatable set, which the walk moves them
	// to; those in constants and inPlace are no operands, and the walk
	// writes them. cell is the call's first entry in the location buffers.
	fixed                       []physReg
	names                       []string
	away                        uint64 // the inputs whose register is outside, constant or not
	outside, constants, inPlace uint64
	result                      physReg
	cell                        int
	// writes are the registers the call writes, as the walk records them.
	writes []physReg
	// stack is set for a 386 call, whose arguments the walk stores to the
	// call area; results is its result's words, in EAX and EDX.
	stack   bool
	results int
	memory  bool // the arguments are Memory uses: their words exceed four
	// clobbers is what Online keeps the values live past the call out of:
	// the effect's clobbers, the fixed registers and every vector register,
	// whose values the direct walk stores around any call. homes are the
	// registers whose homes the call overwrites.
	clobbers, homes backend.RegisterSet
	// moved is set when the result register is a reserved home: the result
	// then takes a register of its own, which the walk moves it to before
	// restoring the home. saved are the homes the walk saves and restores.
	moved bool
	saved []machine.Home
}

// isCall reports whether op is a call or system call.
func isCall(op machine.SelectedOpcode) bool { return op == mCallC || op == mCallGo || op == mSyscall }

// planOnlineCalls reads each call of s.program into s.temps.onlineCalls, with
// the effect the direct walk checks, and reports whether Online can place
// them; otherwise the direct walk takes the program, and reports what it
// rejects.
func planOnlineCalls(s *onlineSetup) bool {
	p, t, target := &s.program, s.temps, s.target
	calls := t.onlineCalls[:0]
	cells := 0
	for pos := range p.Instructions {
		if ins := &p.Instructions[pos]; isCall(ins.Op) {
			calls = append(calls, onlineCall{position: pos})
			cells += len(ins.Args) + 2
		}
	}
	t.onlineCalls, t.onlineCallHomes = calls, t.onlineCallHomes[:0]
	if len(calls) == 0 {
		return true
	}
	// The builder's effects keep pointers into these, sized up front and
	// filled as describeOnlineCall adds each call.
	resetBuffer(&t.onlineCallEffects, len(calls))
	resetBuffer(&t.onlineCallLocations, cells)
	words := resetBuffer(&t.onlineCallWords, cells)
	fixed := resetBuffer(&t.onlineCallFixed, cells)
	inputNames := resetBuffer(&t.onlineCallNames, cells)
	names := physicalRegistersByArchitecture[target.Architecture()]
	hasLink := target.ReturnAddress().InRegister()
	linkReg, linkIsGeneral := target.ReturnAddress().GeneralRegister()
	writes := t.onlineCallWrites[:0]
	var vectors [64]backend.Register
	nv := 0
	for r := range s.config.Registers.All() {
		if backend.IsVectorRegister(r) {
			vectors[nv] = r
			nv++
		}
	}
	at := 0
	for k := range calls {
		c := &calls[k]
		ins := &p.Instructions[c.position]
		n := len(ins.Args)
		if n == 0 || n > 64 {
			return false
		}
		c.cell = at
		if target.Architecture() == "386" && ins.Op != mSyscall {
			if !plan386Call(s, c, ins, fixed[at:at+n], inputNames[at:at+n], words[at+n:at+n+2]) {
				return false
			}
			at += n + 2
			continue
		}
		for _, id := range ins.Args {
			if class := p.Values[id].Class; class == backend.Float || class == backend.Vector {
				return false
			}
		}
		effect, ok := onlineCallEffect(s, ins)
		if !ok || n > 1+len(effect.ArgumentRegisters) {
			return false
		}
		c.kind, c.effect = effect.Kind, effect
		if c.kind == abi.FunctionCall && hasLink && !s.emitter.linkSavedInFrame() {
			return false // the link register needs a slot of its own
		}
		c.cell = at
		if k > 0 && onlineSameCall(&calls[k-1], c, ins, &p.Instructions[calls[k-1].position]) {
			// Consecutive calls mostly share one effect: its registers
			// are the previous call's.
			prev := &calls[k-1]
			c.fixed, c.names, c.result, c.writes = prev.fixed, prev.names, prev.result, prev.writes
			c.clobbers, c.homes, c.away = prev.clobbers, prev.homes, prev.away
			for i, id := range ins.Args {
				if p.Values[id].Constant {
					c.constants |= 1 << uint(i)
				}
			}
			c.outside = c.away &^ c.constants
			words[at+n] = words[prev.cell+n]
			at += n + 2
			continue
		}
		c.fixed, c.names = fixed[at:at+n], inputNames[at:at+n]
		var clobbers [256]backend.Register
		nc := 0
		for i := range n {
			name := effect.NumberRegister
			if i > 0 {
				name = effect.ArgumentRegisters[i-1]
			} else if ins.Op != mSyscall {
				name = target.RegisterName(target.CallTargetRegister())
			}
			reg, ok := names[name]
			if !ok {
				return false
			}
			c.fixed[i], c.names[i] = reg, name
			clobbers[nc] = reg
			nc++
			inside := s.config.Registers.Contains(reg)
			if !inside && !onlineFixedOutsidePool(target, ins.Op, i, reg) {
				return false
			}
			if !inside {
				c.away |= 1 << uint(i)
			}
			switch {
			case p.Values[ins.Args[i]].Constant:
				c.constants |= 1 << uint(i)
			case !inside:
				c.outside |= 1 << uint(i)
			}
		}
		result, ok := names[effect.ResultRegister]
		if !ok || !s.config.Registers.Contains(result) {
			return false
		}
		c.result = result
		words[at+n] = abi.WordLocation{Storage: abi.Register, Register: effect.ResultRegister}
		at += n + 2
		start := len(writes)
		link := c.kind == abi.FunctionCall && hasLink && linkIsGeneral
		for _, name := range effect.ClobberedRegisters {
			if reg, ok := names[name]; ok && !(link && reg == linkReg) {
				writes = append(writes, reg)
			}
		}
		c.writes = writes[start:len(writes):len(writes)]
		for _, name := range effect.ClobberedRegisters {
			if reg, ok := names[name]; ok && nc < len(clobbers) {
				clobbers[nc] = reg
				nc++
			}
		}
		nc += copy(clobbers[nc:], vectors[:nv])
		c.clobbers = onlineRegisterSet(clobbers[:nc])
		if p.HomePlan != nil {
			// The inputs' registers too: a value read in place from a home
			// there is reloaded from the home's slot.
			nc = copy(clobbers[:], c.fixed)
			every := onlineRegistersByArchitecture[target.Architecture()]
			for _, list := range [][]string{effect.ClobberedRegisters, effect.ClobberedFPRegisters, effect.ArgumentRegisters, {effect.NumberRegister, effect.ResultRegister}} {
				for _, name := range list {
					if reg, ok := every[name]; ok && nc < len(clobbers) {
						clobbers[nc] = reg
						nc++
					}
				}
			}
			c.homes = onlineRegisterSet(clobbers[:nc])
		}
	}
	t.onlineCallWrites = writes
	return true
}

// onlineSameCall reports whether call c, at ins, has the effect of prev, at
// previous, and as many inputs.
func onlineSameCall(prev, c *onlineCall, ins, previous *machineInstruction) bool {
	a, b := &prev.effect, &c.effect
	same := func(x, y []string) bool { return len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0]) }
	return ins.Op == previous.Op && len(ins.Args) == len(previous.Args) && a.ResultRegister == b.ResultRegister && a.NumberRegister == b.NumberRegister &&
		same(a.ClobberedRegisters, b.ClobberedRegisters) && same(a.ClobberedFPRegisters, b.ClobberedFPRegisters) && same(a.ArgumentRegisters, b.ArgumentRegisters)
}

// onlineRegistersByArchitecture names the general registers, as
// physicalRegistersByArchitecture does, and the floating-point ones.
var onlineRegistersByArchitecture = func() map[string]map[string]physReg {
	result := make(map[string]map[string]physReg, len(physicalRegistersByArchitecture))
	for architecture, general := range physicalRegistersByArchitecture {
		names := make(map[string]physReg, len(general)+32)
		for name, r := range general {
			names[name] = r
		}
		target := newMachineTarget(compileOptions{Architecture: architecture})
		for _, r := range floatRegisters(architecture) {
			if name := target.RegisterName(r); name != "" {
				if _, exists := names[name]; !exists {
					names[name] = r
				}
			}
		}
		result[architecture] = names
	}
	return result
}()

// onlineRegisterSet returns the set of list's registers, each once.
func onlineRegisterSet(list []backend.Register) backend.RegisterSet {
	var seen [4]uint64
	n := 0
	for _, r := range list {
		if seen[r/64]&(1<<(r%64)) == 0 && n < 64 {
			seen[r/64] |= 1 << (r % 64)
			list[n] = r
			n++
		}
	}
	return backend.NewRegisterSet(list[:n]...)
}

// onlineFixedOutsidePool reports whether input i of a call op may go to reg
// outside the allocatable set, as the direct walk allows.
func onlineFixedOutsidePool(target machineTarget, op machine.SelectedOpcode, i int, reg physReg) bool {
	arch := target.Architecture()
	return (op == mCallC || op == mCallGo) && i == 0 && reg == target.CallTargetRegister() ||
		op == mCallGo && arch == "riscv64" && i == 9 && reg == 8 ||
		op == mCallGo && arch == "loong64" && i >= 1 && reg >= 4 && reg <= 19 ||
		arch == "ppc64le" && (op == mSyscall && i == 0 && reg == 0 || op == mCallGo && i >= 9 && reg >= 14 && reg <= 17)
}

// onlineCallEffect returns the effect of call ins, a custom one when the
// direct walk accepts it.
func onlineCallEffect(s *onlineSetup, ins *machineInstruction) (abi.CallEffect, bool) {
	t, target := s.temps, s.target
	kind, flavor := abi.SystemCall, abi.C
	if ins.Op == mCallC || ins.Op == mCallGo {
		kind = abi.FunctionCall
	}
	if ins.Op == mCallGo {
		flavor = abi.Go
	}
	t.onlineArgumentWords = selectedArgumentWordsInto(s.program, ins.Args[1:], t.onlineArgumentWords[:0])
	effect, err := cachedCallEffect(abi.CallSpec{Architecture: target.Architecture(), Kind: kind, ABI: flavor, ArgumentCount: len(ins.Args) - 1, ArgumentWords: t.onlineArgumentWords})
	if err != nil {
		return abi.CallEffect{}, false
	}
	if ins.Effect == nil {
		return effect, true
	}
	custom := *ins.Effect
	if kind != abi.FunctionCall || custom.Kind != abi.FunctionCall || custom.ABI != flavor ||
		custom.Architecture != effect.Architecture || custom.ResultRegister != effect.ResultRegister ||
		custom.NumberRegister != "" || custom.RequiresCallStack != effect.RequiresCallStack ||
		custom.StackAlignment != effect.StackAlignment || len(custom.ArgumentRegisters) != len(effect.ArgumentRegisters) {
		return abi.CallEffect{}, false
	}
	for i, name := range custom.ArgumentRegisters {
		if name != effect.ArgumentRegisters[i] {
			return abi.CallEffect{}, false
		}
	}
	names := physicalRegistersByArchitecture[target.Architecture()]
	resultIncluded := false
	for _, name := range custom.ClobberedRegisters {
		if _, ok := names[name]; !ok {
			return abi.CallEffect{}, false
		}
		resultIncluded = resultIncluded || name == custom.ResultRegister
	}
	return custom, resultIncluded
}

// describeOnlineCall adds call k's position to b, with the inputs homes
// reads in place left to the walk, saving the reserved homes of region, those
// in segSet, that it overwrites.
func describeOnlineCall(s *onlineSetup, b onlineBuilder, k int, region int, segSet homeMask, homes *onlineHomePlan, ops []regalloc.Operand) []regalloc.Operand {
	p, t := &s.program, s.temps
	c := &t.onlineCalls[k]
	ins := &p.Instructions[c.position]
	locations, words := t.onlineCallLocations[c.cell:], t.onlineCallWords[c.cell:]
	c.inPlace = 0
	uses := 0
	for i, id := range ins.Args {
		bit := uint64(1) << uint(i)
		switch {
		case c.constants&bit != 0:
			continue
		case homes.inPlace(id, c.position) >= 0:
			c.inPlace |= bit
			continue
		case c.outside&bit != 0 || c.stack && i > 0:
			locations[uses] = abi.ValueLocation{} // an ordinary use
		default:
			words[uses] = abi.WordLocation{Storage: abi.Register, Register: c.names[i]}
			locations[uses] = abi.ValueLocation{Words: words[uses : uses+1]}
		}
		ops = append(ops, regalloc.Operand{Value: id, Memory: c.memory && i > 0})
		uses++
	}
	if ins.Dst >= 0 {
		ops = append(ops, regalloc.Operand{Value: ins.Dst, Def: true})
	}
	effect := &t.onlineCallEffects[k]
	*effect = abi.CallEffect{ArgumentLocations: locations[:uses]}
	// A result register reserved for a home is the home's until the walk
	// restores it: the result takes a register of its own.
	n, results := len(ins.Args), 1
	if c.stack {
		results = c.results
	}
	if c.moved = segSet.contains(c.result) || results == 2 && segSet.contains(2); !c.moved {
		effect.ResultLocation = abi.ValueLocation{Words: words[n : n+results]}
	}
	c.saved = c.saved[:0]
	if plan := p.HomePlan; plan != nil {
		saved := t.onlineCallHomes
		start := len(saved)
		for _, id := range onlineHomeIDs(plan, region) {
			h, ok := plan.Lookup(region, id)
			if !ok || h.Register < 0 || !c.homes.Contains(physReg(h.Register)) {
				continue
			}
			// A home register the allocator has until the variable's first
			// store holds nothing to save.
			if r := physReg(h.Register); segSet.contains(r) || !s.config.Registers.Contains(r) {
				saved = append(saved, h)
			}
		}
		t.onlineCallHomes = saved
		c.saved = saved[start:len(saved):len(saved)]
	}
	pos := b.AddCall(effect, ops...)
	b.SetClobbers(pos, c.clobbers)
	return ops
}

// emitOnlineCall emits call c, whose operands Online has placed: the inputs
// outside the allocatable set, then the constant ones and those read in
// place, which need registers Online may have left a value in, the call, the
// result's move when it has a register of its own and the saved homes'
// reloads. The walk stores the homes before Online places the operands.
func emitOnlineCall(s *onlineSetup, a onlineWalk, c *onlineCall, homes *onlineHomePlan) {
	p, emitter, written := &s.program, s.emitter, s.written
	ins := &p.Instructions[c.position]
	if c.stack {
		emitOnline386Call(s, a, c, homes)
		return
	}
	operand := 0
	for i := range ins.Args {
		switch bit := uint64(1) << uint(i); {
		case (c.constants|c.inPlace)&bit != 0:
			continue
		case c.outside&bit != 0:
			emitter.move(c.fixed[i], a.Operand(c.position, operand).Registers[0])
			written[c.fixed[i]] = true
		}
		operand++
	}
	for i, id := range ins.Args {
		bit := uint64(1) << uint(i)
		switch v := &p.Values[id]; {
		case c.inPlace&bit != 0:
			home := physReg(homes.inPlace(id, c.position))
			if slices.Contains(c.fixed, home) {
				// The inputs took the home register: the saved home
				// still holds the value.
				for _, h := range c.saved {
					if physReg(h.Register) == home {
						emitHomeReload(emitter, c.fixed[i], h.Slot, h.Bits)
						break
					}
				}
			} else if home != c.fixed[i] {
				emitter.move(c.fixed[i], home)
			}
		case c.constants&bit == 0:
			continue
		case v.Symbol != "":
			emitter.symbol(c.fixed[i], v.Symbol)
		default:
			emitter.constant(c.fixed[i], v.Imm)
		}
		written[c.fixed[i]] = true
	}
	switch ins.Op {
	case mSyscall:
		emitter.syscall()
	case mCallC:
		emitter.callC(c.effect)
	default:
		emitter.callGo()
	}
	for _, reg := range c.writes {
		written[reg] = true
	}
	if c.moved && ins.Dst >= 0 {
		dst := a.Operand(c.position, operand).Registers[0]
		emitter.move(dst, c.result)
		written[dst] = true
	}
	for _, h := range c.saved {
		emitHomeReload(emitter, physReg(h.Register), h.Slot, h.Bits)
		written[physReg(h.Register)] = true
	}
}

// plan386Call reads 386 call c at ins as the direct walk checks it: the
// callee goes to EAX, the arguments to the call area, the result comes in
// EAX or EAX:EDX, and a C call overwrites EAX, ECX and EDX, a Go call every
// register.
func plan386Call(s *onlineSetup, c *onlineCall, ins *machineInstruction, fixed []physReg, names []string, result []abi.WordLocation) bool {
	p, t, target := &s.program, s.temps, s.target
	if s.emitter.(*physicalEmitter).stack != abi.StackPlatform {
		return false
	}
	flavor := abi.C
	if ins.Op == mCallGo {
		flavor = abi.Go
	}
	t.onlineArgumentWords = selectedArgumentWordsInto(s.program, ins.Args[1:], t.onlineArgumentWords[:0])
	effect, err := cachedCallEffect(abi.CallSpec{Architecture: target.Architecture(), Kind: abi.FunctionCall, ABI: flavor, ArgumentCount: len(ins.Args) - 1, ArgumentWords: t.onlineArgumentWords})
	if err != nil || online386Words(p, ins.Args[0]) != 1 {
		return false
	}
	for _, id := range ins.Args[1:] {
		if class := p.Values[id].Class; class == backend.Float || class == backend.Vector {
			return false
		}
	}
	c.stack, c.kind, c.effect = true, abi.FunctionCall, abi.CallEffect{}
	c.fixed, c.names, c.result = fixed, names, 0
	c.fixed[0], c.names[0] = 0, target.RegisterName(0)
	words := 0
	for i, id := range ins.Args {
		if p.Values[id].Constant {
			c.constants |= 1 << uint(i)
		} else if i > 0 {
			words += online386Words(p, id)
		}
	}
	c.memory = words > 4
	c.results = 1
	if ins.Dst >= 0 && online386Words(p, ins.Dst) == 2 {
		c.results = 2
	}
	result[0] = abi.WordLocation{Storage: abi.Register, Register: target.RegisterName(0)}
	result[1] = abi.WordLocation{Storage: abi.Register, Register: target.RegisterName(2)}
	var list [64]backend.Register
	n := 0
	for r := range s.config.Registers.All() {
		if flavor == abi.Go || r <= 2 || backend.IsVectorRegister(r) {
			list[n] = r
			n++
		}
	}
	c.clobbers = backend.NewRegisterSet(list[:n]...)
	writes := t.onlineCallWrites
	start := len(writes)
	for r := range s.config.Registers.All() {
		if flavor == abi.Go || r <= 2 {
			writes = append(writes, r)
		}
	}
	t.onlineCallWrites = writes
	c.writes = writes[start:len(writes):len(writes)]
	if p.HomePlan != nil {
		var homes [256]backend.Register
		nh := copy(homes[:], c.fixed[:1])
		every := onlineRegistersByArchitecture[target.Architecture()]
		for _, list := range [][]string{effect.ClobberedRegisters, effect.ClobberedFPRegisters, effect.ArgumentRegisters, {effect.NumberRegister, effect.ResultRegister}} {
			for _, name := range list {
				if reg, ok := every[name]; ok && nh < len(homes) {
					homes[nh] = reg
					nh++
				}
			}
		}
		c.homes = onlineRegisterSet(homes[:nh])
	}
	return true
}

// emitOnline386Call emits 386 call c: it stores the arguments to the call
// area, puts a constant callee or one read in place in EAX, calls, and for a
// Go call loads the result from the call area.
func emitOnline386Call(s *onlineSetup, a onlineWalk, c *onlineCall, homes *onlineHomePlan) {
	p, emitter, written := &s.program, s.emitter, s.written
	ins := &p.Instructions[c.position]
	operand := 0
	if c.constants&1 == 0 && c.inPlace&1 == 0 {
		operand = 1 // the callee, in EAX
	}
	// Arguments in registers first; then constants and Memory uses go
	// through ECX, which the call overwrites and nothing past it holds.
	offsets := func(i int) uint64 {
		offset := uint64(0)
		for _, id := range ins.Args[1:i] {
			offset += 4 * uint64(online386Words(p, id))
		}
		return offset
	}
	operands := operand
	for i := 1; i < len(ins.Args); i++ {
		bit := uint64(1) << uint(i)
		var g regalloc.Group
		switch {
		case c.constants&bit != 0:
			continue
		case c.inPlace&bit != 0:
			g = regalloc.SingleRegister(physReg(homes.inPlace(ins.Args[i], c.position)))
		case c.memory:
			operands++
			continue
		default:
			g = a.Operand(c.position, operands)
			operands++
		}
		for w := range int(g.Words) {
			emitter.storeOffset(4, g.Registers[w], offsets(i)+uint64(4*w), ir.ByteOrderNative)
		}
	}
	operands = operand
	for i := 1; i < len(ins.Args); i++ {
		bit := uint64(1) << uint(i)
		id := ins.Args[i]
		v := &p.Values[id]
		switch {
		case c.constants&bit != 0:
			for w := range online386Words(p, id) {
				if v.Symbol != "" && w == 0 {
					emitter.symbol(1, v.Symbol)
				} else {
					emitter.constant(1, uint64(uint32(v.Imm>>(32*w))))
				}
				emitter.storeOffset(4, 1, offsets(i)+uint64(4*w), ir.ByteOrderNative)
			}
			written[1] = true
			continue
		case c.inPlace&bit != 0:
			continue
		case c.memory:
			loc := a.Memory(c.position, operands)
			for w := range online386Words(p, id) {
				if loc.Slot >= 0 {
					emitter.reload(1, loc.Slot+w)
				} else {
					emitter.incomingWord(1, loc.Incoming[w])
				}
				emitter.storeOffset(4, 1, offsets(i)+uint64(4*w), ir.ByteOrderNative)
			}
			written[1] = true
		}
		operands++
	}
	offset := offsets(len(ins.Args))
	operand = operands
	callee := &p.Values[ins.Args[0]]
	switch {
	case c.inPlace&1 != 0:
		emitter.move(0, physReg(homes.inPlace(ins.Args[0], c.position)))
		written[0] = true
	case c.constants&1 != 0 && callee.Symbol != "":
		emitter.symbol(0, callee.Symbol)
		written[0] = true
	case c.constants&1 != 0:
		emitter.constant(0, callee.Imm)
		written[0] = true
	}
	if ins.Op == mCallGo {
		emitter.callGo()
		emitter.loadOffset(0, 4, offset, ir.ByteOrderNative)
		emitter.loadOffset(2, 4, offset+4, ir.ByteOrderNative)
	} else {
		emitter.callC(abi.CallEffect{})
	}
	for _, reg := range c.writes {
		written[reg] = true
	}
	if c.moved && ins.Dst >= 0 {
		dst := a.Operand(c.position, operand)
		if c.results == 2 {
			emitPairMove386(s, dst, regalloc.Group{Registers: [2]physReg{0, 2}, Words: 2})
		} else {
			emitter.move(dst.Registers[0], 0)
			written[dst.Registers[0]] = true
		}
	}
	for _, h := range c.saved {
		emitHomeReload(emitter, physReg(h.Register), h.Slot, h.Bits)
		written[physReg(h.Register)] = true
	}
}

// saveOnlineCallHomes stores the homes call c overwrites.
func saveOnlineCallHomes(s *onlineSetup, c *onlineCall) {
	for _, h := range c.saved {
		emitHomeSpill(s.emitter, physReg(h.Register), h.Slot, h.Bits)
	}
}
