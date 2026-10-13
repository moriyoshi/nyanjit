package compile

import (
	"math/bits"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// Physical record kinds. The engine emits them; the architecture sizes and
// encodes them.
const (
	pkLabel      uint8 = iota // imm: label
	pkMove                    // a <- b, any banks
	pkConst                   // a <- imm
	pkSymbol                  // a <- symbols[imm]
	pkSpill                   // slot imm <- a
	pkReload                  // a <- slot imm
	pkOp                      // IR operation sub at width w: a <- b, c, d (and imm)
	pkOpImm                   // IR operation sub with a folded constant operand: a <- b, imm
	pkCmp                     // flags <- b ? c
	pkCmpImm                  // flags <- b ? imm
	pkJump                    // to label imm
	pkJumpIf                  // to label imm if condition sub
	pkJumpIfLong              // the long form of pkJumpIf
	pkCall                    // indirect call through the call target register; sub: the IR call opcode
	pkSyscall                 // system call
	pkFrameEnter              // imm: frame bytes
	pkFrameLeave              // imm: frame bytes
	pkRet
	pkLiteral // floating-point a <- the literal pool's 64-bit entry; imm: the value, then its index; sub: 1 for the long form
)

// bPhys is one physical operation (16 bytes). Registers use the engine's
// numbering; sz is the encoded size, or zero for the architecture to compute.
type bPhys struct {
	imm  uint64
	k    uint8
	sub  uint8
	w    uint8
	a, b int8
	c, d int8
	sz   uint8
}

// lowerRecord allocates and lowers one record.
func (e *baselineEngine) lowerRecord(i uint32) error {
	a := &e.alloc
	r := &e.recs[i]
	shape := &baselineShapes[r.op]
	k := 3 * int(i-e.blocks[a.block].first)
	if shape.pure && a.vs[i].next == noValue {
		// Dead pure value: no code, only its operands advance.
		ops := [3]uint32{r.a, r.b, r.c}
		for j := 0; j < int(shape.operands); j++ {
			if j > 0 && ops[j] == ops[0] || j > 1 && ops[j] == ops[1] {
				continue
			}
			e.advance(ops[j], a.nu[k+j])
		}
		return nil
	}
	switch r.op {
	case ir.OpParam, ir.OpLabelParam, ir.OpConst, ir.OpScopeEnter, ir.OpScopeLeave:
		if r.op == ir.OpLabelParam || r.op == ir.OpParam {
			if a.vs[i].reg < 0 && a.vs[i].fl&vfStored == 0 && a.vs[i].next != noValue {
				return baselineUnsupported("parameter %d without an entry location", i)
			}
		}
		return nil
	case bOpCopy:
		e.copyValue(i, r, k)
		return nil
	case ir.OpSet:
		e.setVar(i, r, k)
		return nil
	case ir.OpCallC, ir.OpCallGo, ir.OpSyscall:
		if s, ok := e.arch.(baselineStackArch); ok {
			return s.call(e, i, r)
		}
		return e.call(i, r)
	}
	if shape.simple && !e.m.wide {
		return e.lowerSimple(i, r, shape, k)
	}
	var src [3]int8
	lock := uint64(0)
	ops := [3]uint32{r.a, r.b, r.c}
	for j := 0; j < int(shape.operands); j++ {
		v := ops[j]
		if e.isConst(v) && e.recs[v].fl&rfSymbol == 0 && e.arch.foldable(r, j, e.recs[v].imm) {
			src[j] = noReg
			continue
		}
		bank := bankGP
		if r.fpu&(1<<j) != 0 || e.m.wide && e.valueBits(v) == 64 {
			bank = bankFP
		}
		src[j] = e.use(v, bank, &lock)
	}
	for j := 0; j < int(shape.operands); j++ {
		if j > 0 && ops[j] == ops[0] || j > 1 && ops[j] == ops[1] {
			continue
		}
		e.advance(ops[j], a.nu[k+j])
	}
	dst := noReg
	if shape.value {
		bank := bankGP
		if r.fl&rfDefFP != 0 {
			bank = bankFP
		}
		dst = e.def(i, bank, 0)
	}
	if err := e.arch.lower(e, r, dst, src); err != nil {
		return err
	}
	if r.op == ir.OpFCompare && a.out == &a.main {
		a.fcmp, a.fcmpBlock, a.fcmpAt = int32(i), a.block, int32(len(a.main))
	}
	// Expansions may use the scratch registers; the report says so.
	a.flags = true
	a.used |= regBit(e.m.transit) | regBit(e.m.cycle)
	if shape.fpUse != 0 || shape.defFP {
		a.used |= regBit(e.m.scratchFP)
	}
	if shape.value {
		e.defined(i)
	}
	return nil
}

// copyValue lowers a copy (a resolved OpGet or repeated label parameter
// read): the copy takes over the source's register when the source dies.
func (e *baselineEngine) copyValue(i uint32, r *bRec, k int) {
	a := &e.alloc
	v := r.a
	next := a.nu[k]
	s := &a.vs[v]
	switch {
	case e.isConst(v):
		// The copy is the constant.
		*r = e.recs[v]
	case s.reg >= 0 && next == noValue && a.regVal[s.reg] == v:
		reg := s.reg
		e.release(v)
		a.regVal[reg], a.vs[i].reg = i, reg
		a.free &^= regBit(reg)
		e.defined(i)
		return
	case s.reg >= 0:
		dst := e.def(i, regBank(s.reg), regBit(s.reg))
		e.put(bPhys{k: pkMove, a: dst, b: s.reg})
		e.advance(v, next)
		e.defined(i)
		return
	default:
		bank := bankGP
		if a.vs[i].fl&vfFPUse != 0 || e.m.wide && r.bits == 64 {
			bank = bankFP
		}
		dst := e.def(i, bank, 0)
		e.load(dst, v)
		e.advance(v, next)
		e.defined(i)
		return
	}
	e.advance(v, next)
}

// setVar lowers OpSet: a pinned part moves the value into its register,
// another part records the value as its dirty content.
func (e *baselineEngine) setVar(i uint32, r *bRec, k int) {
	a := &e.alloc
	p := e.vars[r.x].part + int32(r.imm)
	v := r.a
	next := a.nu[k]
	if pr := a.pinReg[p]; pr >= 0 {
		if a.vs[v].reg != pr {
			if occ := a.regVal[pr]; occ != noValue {
				if a.vs[occ].next != noValue && a.vstamp[occ] == a.stamp {
					t := e.allocReg(regBank(pr), regBit(pr), -1, false)
					e.put(bPhys{k: pkMove, a: t, b: pr})
					a.regVal[pr] = noValue
					e.bind(occ, t)
				} else {
					e.unbind(pr)
				}
			}
			e.load(pr, v)
		}
		a.used |= regBit(pr)
		e.advance(v, next)
		return
	}
	if old := a.vs[v].vpart; old >= 0 && old != p && a.varCur[old] == v && a.varDirty[old] {
		e.writeBack(old)
	}
	a.varCur[p], a.varDirty[p] = v, true
	a.vs[v].vpart = p
	e.advance(v, next)
}

// callEffect returns the effect of call record r, checking a custom effect
// against the target's convention as Lower does.
func (e *baselineEngine) callEffect(r *bRec) (abi.CallEffect, error) {
	kind, flavor := abi.FunctionCall, abi.C
	switch r.op {
	case ir.OpSyscall:
		kind = abi.SystemCall
	case ir.OpCallGo:
		flavor = abi.Go
	}
	effect, err := cachedCallEffect(abi.CallSpec{Architecture: e.target.architecture, Kind: kind, ABI: flavor, ArgumentCount: int(r.n) - 1})
	if err != nil {
		return effect, err
	}
	if r.fl&rfEffect == 0 {
		return effect, nil
	}
	custom := &e.effects[r.imm]
	mismatch := kind != abi.FunctionCall || custom.Kind != abi.FunctionCall || custom.ABI != flavor ||
		custom.Architecture != effect.Architecture || custom.ResultRegister != effect.ResultRegister ||
		custom.NumberRegister != "" || custom.RequiresCallStack != effect.RequiresCallStack ||
		custom.StackAlignment != effect.StackAlignment || len(custom.ArgumentRegisters) != len(effect.ArgumentRegisters)
	if !mismatch {
		for i, name := range custom.ArgumentRegisters {
			mismatch = mismatch || name != effect.ArgumentRegisters[i]
		}
		included := false
		for _, name := range custom.ClobberedRegisters {
			if _, ok := e.arch.register(name); !ok {
				mismatch = true
			}
			included = included || name == custom.ResultRegister
		}
		mismatch = mismatch || !included
	}
	if mismatch {
		return effect, callEffectMismatch(newMachineTarget(e.options))
	}
	// Allocation keeps the conservative clobbers; the report uses both.
	for _, name := range custom.ClobberedRegisters {
		if reg, ok := e.arch.register(name); ok {
			e.alloc.callers |= regBit(reg)
		}
	}
	return effect, nil
}

// call lowers an outbound call or system call.
func (e *baselineEngine) call(i uint32, r *bRec) error {
	a := &e.alloc
	if e.options.Stack != abi.StackPlatform {
		return ErrStackRequired
	}
	effect, err := e.callEffect(r)
	if err != nil {
		return err
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
	dsts := [24]int8{}
	if r.op == ir.OpSyscall {
		reg, ok := e.arch.register(effect.NumberRegister)
		if !ok {
			return ErrBackendLimit
		}
		dsts[0] = reg
	} else {
		dsts[0] = e.m.callTarget
	}
	if len(effect.ArgumentRegisters)+1 > len(dsts) || len(effect.ArgumentRegisters) != len(args)-1 {
		return ErrBackendLimit
	}
	for k, name := range effect.ArgumentRegisters {
		reg, ok := e.arch.register(name)
		if !ok {
			return ErrBackendLimit
		}
		dsts[k+1] = reg
	}
	liveAfter := func(v uint32) bool {
		for j, x := range args {
			if x == v {
				return a.argNu[int(r.x)+j] != noValue
			}
		}
		return a.vs[v].next != noValue
	}
	// Argument registers the call preserves are still overwritten when they
	// receive another value (amd64 system calls).
	over := clobber
	for k, v := range args {
		if r := dsts[k]; a.regVal[r] != v {
			over |= regBit(r)
		}
	}
	// Values live after the call leave the clobbered registers: to a free
	// callee-saved register, or to memory.
	for m := ^a.free & e.m.allocatable & over; m != 0; m &= m - 1 {
		reg := int8(trailingZeros(m))
		v := a.regVal[reg]
		if v == noValue || !liveAfter(v) {
			continue
		}
		if t := a.free & e.m.allocatable &^ over &^ a.pinned & bankMask(regBank(reg)); t != 0 {
			to := int8(trailingZeros(t))
			e.put(bPhys{k: pkMove, a: to, b: reg})
			a.regVal[reg] = noValue
			a.free |= regBit(reg)
			e.bind(v, to)
			continue
		}
		e.evict(reg)
	}
	for k, v := range args {
		e.addMove(int16(dsts[k]), v)
		a.used |= regBit(dsts[k])
	}
	e.resolveMoves()
	if fixer, ok := e.arch.(baselineCallFixer); ok {
		fixer.fixCallArguments(e, r, args, dsts[:len(args)])
	}
	if r.op == ir.OpSyscall {
		e.put(bPhys{k: pkSyscall})
	} else {
		e.put(bPhys{k: pkCall, sub: uint8(r.op)})
	}
	a.hasCall, a.flags = true, true
	a.used |= clobber
	for m := over & e.m.allocatable; m != 0; m &= m - 1 {
		reg := int8(trailingZeros(m))
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
	if a.vs[i].next != noValue {
		res, ok := e.arch.register(effect.ResultRegister)
		if !ok {
			return ErrBackendLimit
		}
		e.bind(i, res)
		e.defined(i)
	}
	return nil
}

// terminate lowers the block's terminator and its edges.
func (e *baselineEngine) terminate(b int32) error {
	a := &e.alloc
	blk := &e.blocks[b]
	// Homes that edges may overwrite are vacated before any branch, so
	// every path sees the same locations.
	e.forEachEndPart(b, func(p int32) {
		if a.varDirty[p] {
			e.vacate(p)
		}
	})
	switch blk.term {
	case ir.TermReturn:
		if s, ok := e.arch.(baselineStackArch); ok {
			if err := s.returnValue(e, blk); err != nil {
				return err
			}
		} else {
			e.addMove(int16(e.m.results[0]), blk.result)
			if blk.parts {
				e.addMove(int16(e.m.results[1]), blk.resultHi)
			}
			e.resolveMoves()
		}
		if a.next >= 0 {
			e.put(bPhys{k: pkJump, imm: uint64(a.epilogue)})
		}
	case ir.TermJump:
		e.edge(b, blk.to, e.args[blk.argStart:blk.argStart+blk.nArgs])
		e.resolveMoves()
		if blk.to != a.next {
			e.put(bPhys{k: pkJump, imm: uint64(e.blocks[blk.to].label)})
		}
	case ir.TermBranch:
		to, other := blk.to, blk.other
		toArgs := e.args[blk.argStart : blk.argStart+blk.nArgs]
		otherArgs := e.args[blk.argOther : blk.argOther+blk.nOther]
		negate := false
		if to == a.next && other != a.next {
			to, other, toArgs, otherArgs, negate = other, to, otherArgs, toArgs, true
		}
		// The taken edge: straight to its block, or through a stub that
		// holds its moves.
		e.edge(b, to, toArgs)
		taken := e.blocks[to].label
		if len(a.moves) != 0 {
			stub := e.newLabel()
			main := a.out
			a.out = &a.stubs
			e.put(bPhys{k: pkLabel, imm: uint64(stub)})
			e.resolveMoves()
			e.put(bPhys{k: pkJump, imm: uint64(taken)})
			a.out = main
			taken = stub
		}
		fall := e.newLabel()
		if negate {
			e.branch(blk.pred, fall, taken, fall)
		} else {
			e.branch(blk.pred, taken, fall, fall)
		}
		e.put(bPhys{k: pkLabel, imm: uint64(fall)})
		e.edge(b, other, otherArgs)
		e.resolveMoves()
		if other != a.next {
			e.put(bPhys{k: pkJump, imm: uint64(e.blocks[other].label)})
		}
		a.flags = true
	}
	return nil
}

// edge queues the moves of the edge from the current block to t, fixing t's
// entry state if this is its first arriving edge.
func (e *baselineEngine) edge(b, t int32, args []uint32) {
	a := &e.alloc
	tb := &e.blocks[t]
	if !tb.fixed {
		e.fixState(t, args)
	}
	for _, en := range a.states[tb.stateStart : tb.stateStart+tb.stateN] {
		switch en.kind {
		case eParam:
			e.addMove(en.loc, args[en.id])
		case eValue:
			e.addMove(en.loc, en.id)
			if s := &a.vs[en.id]; en.stored && s.fl&vfStored == 0 {
				e.addMove(int16(bSlotLoc+e.homeOf(en.id)), en.id)
			}
		case ePart:
			p := int32(en.id)
			if c := a.varCur[p]; c != noValue {
				e.addMove(en.loc, c)
			} else {
				src := int16(bSlotLoc + e.varHomeOf(p))
				if src != en.loc {
					a.moves = append(a.moves, bMove{dst: en.loc, src: src, value: a.pseudo(p)})
				}
			}
		}
	}
	// Live-in values the target expects in their homes are stored there
	// unless this path already did.
	if a.words != 0 {
		for _, en := range a.states[tb.stateStart : tb.stateStart+tb.stateN] {
			if en.kind == eValue {
				a.vs[en.id].fl |= vfEntry
			}
		}
		in := a.liveIn[int(t)*a.words : (int(t)+1)*a.words]
		for w, word := range in {
			for word != 0 {
				x := w*64 + bits.TrailingZeros64(word)
				word &= word - 1
				v := a.cross[x]
				s := &a.vs[v]
				if s.fl&vfEntry != 0 {
					s.fl &^= vfEntry
					continue
				}
				if !e.isConst(v) && s.fl&vfStored == 0 {
					e.addMove(int16(bSlotLoc+e.homeOf(v)), v)
				}
			}
		}
	}
	// Dirty parts the target keeps in their homes are stored.
	e.forEachEndPart(b, func(p int32) {
		if !a.varDirty[p] || !e.partActive(p, tb.first) {
			return
		}
		for _, en := range a.states[tb.stateStart : tb.stateStart+tb.stateN] {
			if en.kind == ePart && int32(en.id) == p {
				return
			}
		}
		e.addMove(int16(bSlotLoc+e.varHomeOf(p)), a.varCur[p])
	})
}

// branch emits predicate p: control goes to label t when it holds and to f
// otherwise; next is the label that follows the code.
func (e *baselineEngine) branch(p int32, t, f, next int32) {
	n := &e.preds[p]
	switch n.kind {
	case ir.PredicateNot:
		e.branch(n.left, f, t, next)
	case ir.PredicateAnd:
		mid := e.newLabel()
		e.branch(n.left, mid, f, mid)
		e.put(bPhys{k: pkLabel, imm: uint64(mid)})
		e.branch(n.right, t, f, next)
	case ir.PredicateOr:
		mid := e.newLabel()
		e.branch(n.left, t, mid, mid)
		e.put(bPhys{k: pkLabel, imm: uint64(mid)})
		e.branch(n.right, t, f, next)
	default:
		// The compare records carry the operand width and the condition
		// for targets that compare 64-bit values as pairs.
		w, cond := e.valueBits(n.value), n.cond
		e.branches = true
		if c, ok := e.fusedFloatBranch(n); ok {
			e.jumpIf(c, t, f, next)
			return
		}
		left := e.predicateOperand(n.value, e.m.transit)
		if n.withValue && e.isConst(n.other) && e.recs[n.other].fl&rfSymbol == 0 && e.recs[n.other].imm <= 4095 {
			// A small constant operand compares as an immediate.
			e.put(bPhys{k: pkCmpImm, sub: uint8(cond), w: w, b: left, imm: e.recs[n.other].imm})
		} else if n.withValue {
			right := e.predicateOperand(n.other, e.m.cycle)
			e.put(bPhys{k: pkCmp, sub: uint8(cond), w: w, b: left, c: right})
		} else if n.imm >= -4095 && n.imm <= 4095 {
			e.put(bPhys{k: pkCmpImm, sub: uint8(cond), w: w, b: left, imm: uint64(n.imm)})
		} else {
			e.put(bPhys{k: pkConst, a: e.m.cycle, imm: uint64(n.imm)})
			e.put(bPhys{k: pkCmp, sub: uint8(cond), w: w, b: left, c: e.m.cycle})
		}
		e.jumpIf(cond, t, f, next)
	}
}

// jumpIf goes to label t when cond holds after a compare and to f
// otherwise; next is the label that follows the code.
func (e *baselineEngine) jumpIf(cond ir.BranchCondition, t, f, next int32) {
	switch next {
	case f:
		e.put(bPhys{k: pkJumpIf, sub: uint8(cond), imm: uint64(t)})
	case t:
		e.put(bPhys{k: pkJumpIf, sub: uint8(cond ^ 1), imm: uint64(f)})
	default:
		e.put(bPhys{k: pkJumpIf, sub: uint8(cond), imm: uint64(t)})
		e.put(bPhys{k: pkJump, imm: uint64(f)})
	}
}

// fusedFloatBranch returns the condition that decides predicate leaf n
// directly from the flags of the OpFCompare it tests, when that compare's
// code is the last flag-setting code in the block.
func (e *baselineEngine) fusedFloatBranch(n *bPred) (ir.BranchCondition, bool) {
	a := &e.alloc
	if n.withValue || n.imm != 0 || n.cond != ir.BranchNotEqual && n.cond != ir.BranchEqual ||
		a.fcmp < 0 || n.value != uint32(a.fcmp) || a.fcmpBlock != a.block || a.out != &a.main {
		return 0, false
	}
	fb, ok := e.arch.(baselineFloatBrancher)
	if !ok {
		return 0, false
	}
	c, ok := fb.floatBranch(e, &e.recs[n.value], a.main[a.fcmpAt:])
	if !ok {
		return 0, false
	}
	if n.cond == ir.BranchEqual {
		c ^= 1
	}
	return c, true
}

// predicateOperand returns a general register holding v without changing
// the allocation: v's register, or scratch loaded from v's location. A wide
// model's comparisons read either bank.
func (e *baselineEngine) predicateOperand(v uint32, scratch int8) int8 {
	s := &e.alloc.vs[v]
	if s.reg >= 0 && (regBank(s.reg) == bankGP || e.m.wide) {
		return s.reg
	}
	e.load(scratch, v)
	return scratch
}

func trailingZeros(m uint64) int { return bits.TrailingZeros64(m) }

// lowerSimple lowers a live integer operation with one or two general
// operands (shape.simple): lowerRecord's path with its common cases inline.
func (e *baselineEngine) lowerSimple(i uint32, r *bRec, shape *opShape, k int) error {
	a := &e.alloc
	var src [3]int8
	lock := uint64(0)
	va, vb := r.a, r.b
	// Operand A is never folded.
	if s := &a.vs[va]; s.reg >= 0 && s.reg < fpBase {
		src[0], lock = s.reg, regBit(s.reg)
	} else {
		src[0] = e.use(va, bankGP, &lock)
	}
	two := shape.operands == 2
	if two {
		if vb < a.nrec && e.recs[vb].op == ir.OpConst && e.recs[vb].fl&rfSymbol == 0 && e.foldable(r, 1, e.recs[vb].imm) {
			src[1] = noReg
		} else if s := &a.vs[vb]; s.reg >= 0 && s.reg < fpBase {
			src[1] = s.reg
			lock |= regBit(s.reg)
		} else {
			src[1] = e.use(vb, bankGP, &lock)
		}
	}
	// Operands advance to their next uses; a dead one in a register of its
	// own only frees the register (release's common case).
	keep := a.pinned | ^e.m.allocatable
	if s := &a.vs[va]; a.nu[k] != noValue {
		s.next = a.nu[k]
	} else if r := s.reg; r >= 0 && a.regVal[r] == va && s.fl&vfPool == 0 {
		s.next, s.reg, a.regVal[r] = noValue, noReg, noValue
		a.free |= regBit(r) &^ keep
	} else {
		s.next = noValue
		e.release(va)
	}
	if two && vb != va {
		if s := &a.vs[vb]; a.nu[k+1] != noValue {
			s.next = a.nu[k+1]
		} else if r := s.reg; r >= 0 && a.regVal[r] == vb && s.fl&vfPool == 0 {
			s.next, s.reg, a.regVal[r] = noValue, noReg, noValue
			a.free |= regBit(r) &^ keep
		} else {
			s.next = noValue
			e.release(vb)
		}
	}
	dst := e.def(i, bankGP, 0)
	switch e.archID {
	case archARM64, archAMD64:
		// The hot operations, written in place with their size.
		p := e.slot()
		p.k, p.sub, p.w, p.a, p.b, p.c, p.imm = pkOp, uint8(r.op), r.bits, dst, src[0], src[1], r.imm
		if src[1] < 0 && two {
			p.k, p.imm = pkOpImm, e.recs[vb].imm
		}
		if e.archID == archARM64 {
			p.sz = uint8(arm64FastOpSize(p))
		} else {
			p.sz = uint8(amd64BaselineArch{}.size(e, p))
		}
	default:
		if err := e.arch.lower(e, r, dst, src); err != nil {
			return err
		}
	}
	a.flags = true
	a.used |= regBit(e.m.transit) | regBit(e.m.cycle)
	// A pure live value has a next use: defined has nothing to release.
	return nil
}

// foldable is e.arch.foldable, called directly for the common targets.
func (e *baselineEngine) foldable(r *bRec, k int, imm uint64) bool {
	switch e.archID {
	case archARM64:
		return arm64BaselineArch{}.foldable(r, k, imm)
	case archAMD64:
		return amd64BaselineArch{}.foldable(r, k, imm)
	}
	return e.arch.foldable(r, k, imm)
}
