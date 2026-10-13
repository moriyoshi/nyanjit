package compile

import (
	"fmt"
	"math/bits"
	"slices"

	"github.com/moriyoshi/nyanjit/ir"
)

// bVal is a value's allocation state (16 bytes).
type bVal struct {
	next  uint32 // next use in the current block, farUse, or noValue
	slot  int32  // memory copy or home, or -1
	vpart int32  // the Var part this value was last Set to, or -1
	reg   int8
	fl    uint8
	_     [2]uint8
}

const (
	vfStored uint8 = 1 << iota // slot holds the value
	vfPool                     // slot is a block-local spill slot
	vfHome                     // slot is the value's permanent cross-block home
	vfFPUse                    // the value's next use reads it in the FP bank
	vfEntry                    // scratch mark: listed in an entry state
)

// bEntry is one element of a block's entry state: where a label parameter,
// a live-in value, or a cached Var part is when control enters the block.
type bEntry struct {
	id   uint32 // parameter index, value ID or Var part
	loc  int16  // register, or bSlotLoc+slot
	kind uint8
	// stored promises that a live-in value's home is also valid: the first
	// edge had written it, and every later edge writes it if needed.
	stored bool
}

const (
	eParam uint8 = iota
	eValue
	ePart
)

// Move locations: registers 0-63, slots from bSlotLoc; a constant source is
// bConstLoc with the constant's record.
const (
	bSlotLoc  = 64
	bConstLoc = -1
)

type bMove struct {
	dst, src int16
	value    uint32 // constant source record
	bank     uint8
}

type baselineAllocState struct {
	vs      []bVal
	vstamp  []uint32
	stamp   uint32
	xid     []int32
	cross   []uint32
	words   int
	liveIn  []uint64
	gen     []uint64
	kill    []uint64
	liveOut []uint64
	nu      []uint32 // three per record of the current block
	argNu   []uint32 // parallel to engine args

	rpo, order, predStart, preds, children, cursor, depth, stack []int32
	crossUses                                                    []uint64

	regVal [64]uint32
	free   uint64 // free allocatable registers
	used   uint64 // registers written
	pinned uint64

	// Var parts.
	pinReg   []int8
	varHome  []int32
	varCur   []uint32
	varDirty []bool
	occupant []uint32
	pstamp   []uint32
	endCur   []uint32
	weight   []uint32
	fpVotes  []int32
	touched  []int32
	cached   []int32 // parts cached at block entry
	pinGP    []int8  // pinOrder's buffers
	pinFP    []int8

	paramVal []uint32 // current block's OpLabelParam values

	states   []bEntry
	moves    []bMove
	nslots   int32
	pool     []int32
	poolFree []int32

	block    int32
	next     int32 // next reachable block in layout, or -1
	labels   int32
	epilogue int32
	out      *[]bPhys
	main     []bPhys
	reserve  int // records reserved for the prologue at the start of main
	stubs    []bPhys
	callers  uint64 // registers clobbered by calls
	hasCall  bool
	flags    bool
	nrec     uint32

	// fcmp is the last OpFCompare lowered in block fcmpBlock; its code ends
	// at main[fcmpAt].
	fcmp, fcmpBlock, fcmpAt int32
}

// pseudo returns the value ID of Var part p's entry content.
func (a *baselineAllocState) pseudo(p int32) uint32 { return a.nrec + uint32(p) }

// slot appends a zeroed record to the current stream and returns it. Writing
// fields in place avoids copying a stack temporary, which stalls store
// forwarding on arm64.
func (e *baselineEngine) slot() *bPhys {
	out := e.alloc.out
	n := len(*out)
	if n == cap(*out) {
		*out = append(*out, bPhys{})
	} else {
		// Reslicing the same expression writes only the length, so the
		// common path needs no write barrier.
		*out = (*out)[:n+1]
		(*out)[n] = bPhys{}
	}
	return &(*out)[n]
}

func (e *baselineEngine) put(p bPhys) {
	// Field by field: copying the temporary whole would load words its
	// byte stores have not yet retired, which stalls store forwarding.
	q := e.slot()
	q.imm, q.k, q.sub, q.w = p.imm, p.k, p.sub, p.w
	q.a, q.b, q.c, q.d, q.sz = p.a, p.b, p.c, p.d, p.sz
	if p.sz == 0 && p.k != pkLabel {
		q.sz = uint8(e.size(q))
	}
}

// size is e.arch.size, called directly for the common targets.
func (e *baselineEngine) size(p *bPhys) int {
	switch e.archID {
	case archARM64:
		return arm64BaselineArch{}.size(e, p)
	case archAMD64:
		return amd64BaselineArch{}.size(e, p)
	}
	return e.arch.size(e, p)
}

func (e *baselineEngine) newLabel() int32 {
	l := e.alloc.labels
	e.alloc.labels++
	return l
}

// allocate runs the allocation pass over every reachable block.
func (e *baselineEngine) allocate() error {
	a := &e.alloc
	nrec := uint32(len(e.recs))
	parts := len(e.partVar)
	a.nrec = nrec
	nv := int(nrec) + parts
	if cap(a.vs) < nv {
		a.vs = make([]bVal, nv, nv+nv/4)
		a.vstamp = make([]uint32, nv, nv+nv/4)
		a.stamp = 0
	}
	a.vs, a.vstamp = a.vs[:nv], a.vstamp[:nv]
	if a.stamp > 1<<31 {
		clear(a.vstamp[:cap(a.vstamp)])
		a.stamp = 0
	}
	for i := range a.vs {
		a.vs[i] = bVal{slot: -1, vpart: -1, reg: noReg}
	}
	a.argNu = growUint32(a.argNu, len(e.args))
	a.pinReg = growInt8(a.pinReg, parts)
	a.varHome = growInt32(a.varHome, parts)
	a.varCur = growUint32(a.varCur, parts)
	a.endCur = growUint32(a.endCur, parts)
	a.occupant = growUint32(a.occupant, parts)
	a.pstamp = growUint32(a.pstamp, parts)
	a.weight = growUint32(a.weight, parts)
	a.fpVotes = growInt32(a.fpVotes, parts)
	if cap(a.varDirty) < parts {
		a.varDirty = make([]bool, parts)
	}
	a.varDirty = a.varDirty[:parts]
	for p := 0; p < parts; p++ {
		a.pinReg[p], a.varHome[p], a.varCur[p], a.occupant[p], a.pstamp[p], a.weight[p], a.fpVotes[p] = noReg, -1, noValue, noValue, 0, 0, 0
		a.varDirty[p] = false
	}
	a.states = a.states[:0]
	a.nslots, a.pool = 0, a.pool[:0]
	a.used, a.pinned, a.callers = 0, 0, 0
	a.hasCall, a.flags = false, false
	a.main, a.stubs = a.main[:0], a.stubs[:0]
	// The prologue goes in front of the body without moving it: finish
	// writes it at the end of this reserve.
	a.reserve = 1 + bits.OnesCount64(e.m.saved)
	for range a.reserve {
		a.main = append(a.main, bPhys{})
	}
	a.labels = int32(len(e.blocks))
	a.epilogue = e.newLabel()
	a.fcmp, a.fcmpBlock = -1, -1
	e.pinVars()
	a.out = &a.main
	// Layout order is segment order; unreachable blocks are dropped.
	last := int32(-1)
	for b := range e.blocks {
		if e.blocks[b].reach {
			last = int32(b)
		}
	}
	for b := int32(0); b <= last; b++ {
		if !e.blocks[b].reach {
			continue
		}
		a.next = -1
		for n := b + 1; n <= last; n++ {
			if e.blocks[n].reach {
				a.next = n
				break
			}
		}
		if err := e.allocBlock(b); err != nil {
			return err
		}
	}
	return nil
}

func growInt8(s []int8, n int) []int8 {
	if cap(s) < n {
		return make([]int8, n, n+n/4)
	}
	return s[:n]
}

// pinVars weighs Var parts by their accesses, scaled by loop depth, and pins
// the heaviest to callee-saved registers.
func (e *baselineEngine) pinVars() {
	a := &e.alloc
	if len(e.partVar) == 0 || e.hasGoCall {
		return
	}
	for b := range e.blocks {
		blk := &e.blocks[b]
		if !blk.reach {
			continue
		}
		w := uint32(1) << (3 * min(uint32(blk.depth), 5))
		for i := blk.first; i < blk.end; i++ {
			r := &e.recs[i]
			if r.op != ir.OpGet && r.op != ir.OpSet {
				continue
			}
			v := &e.vars[r.x]
			if v.immutable {
				continue
			}
			p := v.part + int32(r.imm)
			a.weight[p] += w
			if r.op == ir.OpSet {
				if e.recs[r.a].fl&rfDefFP != 0 {
					a.fpVotes[p]++
				} else {
					a.fpVotes[p]--
				}
			}
		}
	}
	order := a.touched[:0]
	for p := range e.partVar {
		if a.weight[p] >= 8 {
			order = append(order, int32(p))
		}
	}
	slices.SortStableFunc(order, func(x, y int32) int { return int(a.weight[y]) - int(a.weight[x]) })
	gp, fp := 0, 0
	reserve := 0
	pinGP, pinFP := e.m.calleeSavedGP, e.m.calleeSavedFP
	if e.hasCall {
		reserve = 3
	} else if !e.m.wide {
		a.pinGP = e.pinOrder(bankGP, e.m.calleeSavedGP, a.pinGP)
		a.pinFP = e.pinOrder(bankFP, e.m.calleeSavedFP, a.pinFP)
		pinGP, pinFP = a.pinGP, a.pinFP
	}
	for _, p := range order {
		if a.fpVotes[p] > 0 {
			if fp < len(pinFP)-reserve {
				a.pinReg[p] = pinFP[fp]
				fp++
			}
		} else if gp < len(pinGP)-reserve {
			a.pinReg[p] = pinGP[gp]
			gp++
		}
		if r := a.pinReg[p]; r >= 0 {
			a.pinned |= regBit(r)
		}
	}
	a.touched = order[:0]
}

// pinFree is the number of allocatable registers per bank that pinning
// leaves for operands and temporaries.
const pinFree = 5

// pinOrder returns the registers of bank that may hold pinned Vars in a
// function without calls, in the order Vars take them: caller-saved
// registers first, which need no saving because no call clobbers them, then
// the callee-saved ones. Entry parameter and result registers, and those the
// model reserves for fixed-register operations, are left out.
func (e *baselineEngine) pinOrder(bank uint8, saved []int8, buf []int8) []int8 {
	all := e.m.allocatable & bankMask(bank)
	m := all &^ e.m.pinAvoid &^ regBit(e.m.results[0]) &^ regBit(e.m.results[1])
	for k := range e.params {
		if k < len(e.m.params) {
			m &^= regBit(e.m.params[k])
		}
	}
	for _, r := range saved {
		m &^= regBit(r)
	}
	buf = buf[:0]
	for m != 0 {
		r := int8(63 - bits.LeadingZeros64(m))
		m &^= regBit(r)
		buf = append(buf, r)
	}
	buf = append(buf, saved...)
	if limit := max(bits.OnesCount64(all)-pinFree, 0); len(buf) > limit {
		buf = buf[:limit]
	}
	return buf
}

// partActive reports whether Var part p is declared and its region entered
// at position pos.
func (e *baselineEngine) partActive(p int32, pos uint32) bool {
	v := &e.vars[e.partVar[p]]
	if v.immutable || int32(pos) <= v.decl {
		return false
	}
	if v.region == 0 {
		return true
	}
	index, ok := e.scopeIndex[int(v.region)]
	if !ok {
		return false
	}
	s := &e.scopes[index]
	return s.enter < int32(pos) && int32(pos) < s.position
}

func (a *baselineAllocState) liveOutHas(v uint32) bool {
	if v >= a.nrec || a.words == 0 {
		return false
	}
	x := a.xid[v]
	return x >= 0 && a.liveOut[x/64]&(1<<(x%64)) != 0
}

// touch records a use of v at pos during the backward pass and returns the
// use that follows it.
func (e *baselineEngine) touch(v uint32, pos uint32) uint32 {
	a := &e.alloc
	if a.vstamp[v] != a.stamp {
		a.vstamp[v] = a.stamp
		a.vs[v].next = noValue
		if a.liveOutHas(v) {
			a.vs[v].next = farUse
		}
	}
	prev := a.vs[v].next
	a.vs[v].next = pos
	return prev
}

// link is touch for an operand of record i that also records whether the
// use reads the floating-point bank (fp is vfFPUse or 0).
func (e *baselineEngine) link(v, i uint32, fp uint8) uint32 {
	a := &e.alloc
	if a.vstamp[v] != a.stamp {
		prev := e.touch(v, i)
		a.vs[v].fl = a.vs[v].fl&^vfFPUse | fp
		return prev
	}
	s := &a.vs[v]
	prev := s.next
	s.next, s.fl = i, s.fl&^vfFPUse|fp
	return prev
}

// fpUse returns vfFPUse when bit k of r.fpu is set.
func fpUse(r *bRec, k uint) uint8 { return (r.fpu >> k & 1) * vfFPUse }

func (e *baselineEngine) allocBlock(b int32) error {
	a := &e.alloc
	blk := &e.blocks[b]
	a.block = b
	a.stamp++
	// Live-out cross-block values.
	if a.words != 0 {
		out := a.liveOut
		clear(out)
		succ, ns := e.successors(int(b))
		for _, s := range succ[:ns] {
			in := a.liveIn[int(s)*a.words : (int(s)+1)*a.words]
			for w := range out {
				out[w] |= in[w]
			}
		}
	}
	if !blk.fixed && b != 0 {
		e.fixDefault(b)
	}
	e.resolveVars(b)
	e.nextUses(b)
	e.enterBlock(b)
	for i := blk.first; i < blk.end; i++ {
		if err := e.lowerRecord(i); err != nil {
			return err
		}
	}
	if err := e.terminate(b); err != nil {
		return err
	}
	for _, p := range a.touched {
		a.varCur[p], a.varDirty[p] = noValue, false
	}
	for _, p := range a.cached {
		a.varCur[p], a.varDirty[p] = noValue, false
	}
	return nil
}

// resolveVars turns every OpGet of the block into a copy of the Var's
// content: a value Set earlier in the block, the part's entry content, or a
// capture's initial value. Repeated reads of a label parameter become copies.
func (e *baselineEngine) resolveVars(b int32) {
	a := &e.alloc
	blk := &e.blocks[b]
	a.touched = a.touched[:0]
	a.paramVal = growUint32(a.paramVal, int(blk.nparams))
	for j := range a.paramVal {
		a.paramVal[j] = noValue
	}
	if !blk.vars {
		return
	}
	first := blk.first
	// fwd returns the source of a copy made earlier in the block. Uses in
	// the block read the source itself: the copy is materialized only for
	// uses in later blocks, and the source's own lifetime covers the rest.
	// A source that a Set later displaces from its register or home is
	// moved out first (setVar, vacate), so its value stays available.
	fwd := func(v uint32) uint32 {
		if v >= first && v < blk.end && e.recs[v].op == bOpCopy {
			return e.recs[v].a
		}
		return v
	}
	for i := first; i < blk.end; i++ {
		r := &e.recs[i]
		switch r.op {
		case ir.OpGet:
			v := &e.vars[r.x]
			r.op = bOpCopy
			if v.immutable {
				r.a = v.initial[r.imm]
				continue
			}
			p := v.part + int32(r.imm)
			e.touchPart(p)
			r.a = a.endCur[p]
			continue
		case ir.OpLabelParam:
			if j := r.imm; a.paramVal[j] == noValue {
				a.paramVal[j] = i
			} else {
				r.op, r.a = bOpCopy, a.paramVal[j]
			}
			continue
		case ir.OpCallC, ir.OpCallGo, ir.OpSyscall:
			args := e.args[r.x : r.x+uint32(r.n)]
			for k, x := range args {
				args[k] = fwd(x)
			}
			continue
		}
		switch baselineShapes[r.op].operands {
		case 3:
			r.c = fwd(r.c)
			fallthrough
		case 2:
			r.b = fwd(r.b)
			fallthrough
		case 1:
			r.a = fwd(r.a)
		}
		if r.op == ir.OpSet {
			p := e.vars[r.x].part + int32(r.imm)
			e.touchPart(p)
			a.endCur[p] = r.a
		}
	}
	// The terminator's operands.
	switch blk.term {
	case ir.TermJump, ir.TermBranch:
		for k, x := range e.args[blk.argStart : blk.argStart+blk.nArgs] {
			e.args[int(blk.argStart)+k] = fwd(x)
		}
		if blk.term == ir.TermBranch {
			for k, x := range e.args[blk.argOther : blk.argOther+blk.nOther] {
				e.args[int(blk.argOther)+k] = fwd(x)
			}
			work := append(a.stack[:0], blk.pred)
			for len(work) != 0 {
				p := &e.preds[work[len(work)-1]]
				work = work[:len(work)-1]
				switch p.kind {
				case ir.PredicateCompare:
					p.value = fwd(p.value)
					if p.withValue {
						p.other = fwd(p.other)
					}
				case ir.PredicateNot:
					work = append(work, p.left)
				default:
					work = append(work, p.right, p.left)
				}
			}
			a.stack = work[:0]
		}
	case ir.TermReturn:
		blk.result = fwd(blk.result)
		if blk.parts {
			blk.resultHi = fwd(blk.resultHi)
		}
	}
}

func (e *baselineEngine) touchPart(p int32) {
	a := &e.alloc
	if a.pstamp[p] != a.stamp {
		a.pstamp[p] = a.stamp
		a.endCur[p] = a.pseudo(p)
		a.touched = append(a.touched, p)
	}
}

// endParts lists the unpinned parts whose content an edge may transfer:
// those touched in the block and those cached at its entry.
func (e *baselineEngine) forEachEndPart(b int32, f func(p int32)) {
	a := &e.alloc
	blk := &e.blocks[b]
	for _, p := range a.touched {
		if a.pinReg[p] < 0 && e.partActive(p, blk.end) {
			f(p)
		}
	}
	for _, p := range a.cached {
		if a.pstamp[p] != a.stamp && a.pinReg[p] < 0 && e.partActive(p, blk.end) {
			f(p)
		}
	}
}

// nextUses runs the block's backward pass: next uses for every operand, and
// the bank of each value's first use.
func (e *baselineEngine) nextUses(b int32) {
	a := &e.alloc
	blk := &e.blocks[b]
	T := blk.end
	// Entry-cached parts are known before the pass; mark them so their
	// pseudo values get uses at the terminator.
	a.cached = a.cached[:0]
	if blk.stateStart >= 0 {
		for _, en := range a.states[blk.stateStart : blk.stateStart+blk.stateN] {
			if en.kind == ePart {
				a.cached = append(a.cached, int32(en.id))
			}
		}
	}
	// The block's own values start without uses; the terminator and the
	// backward pass then link their uses.
	vs, vstamp, stamp := a.vs, a.vstamp, a.stamp
	for i := blk.first; i < blk.end; i++ {
		vstamp[i] = stamp
		vs[i].next = noValue
	}
	if a.words != 0 {
		for i := blk.first; i < blk.end; i++ {
			if a.liveOutHas(i) {
				vs[i].next = farUse
			}
		}
	}
	switch blk.term {
	case ir.TermJump:
		for _, v := range e.args[blk.argStart : blk.argStart+blk.nArgs] {
			e.touch(v, T)
		}
	case ir.TermBranch:
		for _, v := range e.args[blk.argStart : blk.argStart+blk.nArgs] {
			e.touch(v, T)
		}
		for _, v := range e.args[blk.argOther : blk.argOther+blk.nOther] {
			e.touch(v, T)
		}
		e.predicateUses(blk.pred, func(v uint32) error { e.touch(v, T); return nil })
	case ir.TermReturn:
		e.touch(blk.result, T)
		if blk.parts {
			e.touch(blk.resultHi, T)
		}
	}
	if blk.term != ir.TermReturn {
		e.forEachEndPart(b, func(p int32) {
			c := a.pseudo(p)
			if a.pstamp[p] == a.stamp {
				c = a.endCur[p]
			}
			e.touch(c, T)
		})
	}
	n := int(blk.end - blk.first)
	a.nu = growUint32(a.nu, 3*n)
	nu := a.nu
	for i := blk.end; i > blk.first; {
		i--
		r := &e.recs[i]
		k := 3 * int(i-blk.first)
		shape := &baselineShapes[r.op]
		// A repeated operand shares its first occurrence's next use.
		if n := shape.operands; n != 0 {
			if v := r.a; vstamp[v] == stamp {
				s := &vs[v]
				nu[k], s.next, s.fl = s.next, i, s.fl&^vfFPUse|fpUse(r, 0)
			} else {
				nu[k] = e.link(v, i, fpUse(r, 0))
			}
			if n >= 2 {
				if v := r.b; v == r.a {
					nu[k+1] = nu[k]
				} else if vstamp[v] == stamp {
					s := &vs[v]
					nu[k+1], s.next, s.fl = s.next, i, s.fl&^vfFPUse|fpUse(r, 1)
				} else {
					nu[k+1] = e.link(v, i, fpUse(r, 1))
				}
				if n == 3 {
					switch r.c {
					case r.a:
						nu[k+2] = nu[k]
					case r.b:
						nu[k+2] = nu[k+1]
					default:
						nu[k+2] = e.link(r.c, i, fpUse(r, 2))
					}
				}
			}
		}
		if shape.call {
			args := e.args[r.x : r.x+uint32(r.n)]
			for j, v := range args {
				dup := -1
				for q := 0; q < j; q++ {
					if args[q] == v {
						dup = q
						break
					}
				}
				if dup >= 0 {
					a.argNu[int(r.x)+j] = a.argNu[int(r.x)+dup]
					continue
				}
				a.argNu[int(r.x)+j] = e.touch(v, i)
				a.vs[v].fl &^= vfFPUse
			}
		}
	}
}

// emptyRegVal is a register file holding no value; copying it is faster
// than storing each entry.
var emptyRegVal = func() (r [64]uint32) {
	for i := range r {
		r[i] = noValue
	}
	return r
}()

// enterBlock sets up the register file from the block's entry state.
func (e *baselineEngine) enterBlock(b int32) {
	a := &e.alloc
	blk := &e.blocks[b]
	a.regVal = emptyRegVal
	a.free = e.m.allocatable &^ a.pinned
	a.poolFree = append(a.poolFree[:0], a.pool...)
	if b != 0 {
		e.put(bPhys{k: pkLabel, imm: uint64(blk.label)})
	}
	// Live-in values start in their homes unless the state says otherwise.
	if a.words != 0 {
		in := a.liveIn[int(b)*a.words : (int(b)+1)*a.words]
		for w, word := range in {
			for word != 0 {
				x := w*64 + bits.TrailingZeros64(word)
				word &= word - 1
				v := a.cross[x]
				a.vs[v].reg = noReg
				if !e.isConst(v) && (blk.deflt || a.vs[v].fl&vfHome != 0) {
					// Every edge into the block stores the value
					// unless the entry state keeps it in a register.
					e.homeOf(v)
					a.vs[v].fl |= vfStored
				}
				if a.vstamp[v] != a.stamp {
					a.vstamp[v] = a.stamp
					a.vs[v].next = noValue
					if a.liveOutHas(v) {
						a.vs[v].next = farUse
					}
				}
			}
		}
	}
	if s, ok := e.arch.(baselineStackArch); ok && b == 0 {
		s.enterParams(e)
	} else if b == 0 {
		for k, v := range e.params {
			if k >= len(e.m.params) {
				break
			}
			r := e.m.params[k]
			if a.vs[v].next == noValue || a.vstamp[v] != a.stamp {
				continue
			}
			e.bind(v, r)
			if bits := e.recs[v].bits; bits < 64 {
				// Callers need not extend narrow arguments.
				e.put(bPhys{k: pkOp, sub: uint8(ir.OpCastUnsigned), w: bits, a: r, b: r, d: 64})
			}
			e.defined(v)
		}
	} else {
		for _, en := range a.states[blk.stateStart : blk.stateStart+blk.stateN] {
			switch en.kind {
			case eParam:
				v := a.paramVal[en.id]
				if v == noValue || a.vstamp[v] != a.stamp || a.vs[v].next == noValue {
					continue
				}
				if en.loc < bSlotLoc {
					e.bind(v, int8(en.loc))
					e.defined(v)
				} else {
					s := &a.vs[v]
					s.slot, s.fl = int32(en.loc-bSlotLoc), s.fl|vfStored
					if a.xid[v] >= 0 {
						s.fl |= vfHome
					}
				}
			case eValue:
				v := en.id
				// The register is authoritative; the home is valid only
				// where the state promises it.
				a.vs[v].fl &^= vfStored
				if en.stored {
					a.vs[v].fl |= vfStored
				}
				if a.vs[v].next != noValue {
					e.bind(v, int8(en.loc))
				}
			case ePart:
				p := int32(en.id)
				pv := a.pseudo(p)
				a.vs[pv] = bVal{next: a.vs[pv].next, slot: -1, vpart: p, reg: noReg}
				// The home does not hold a cached part's content.
				a.occupant[p] = noValue
				if a.vstamp[pv] != a.stamp {
					a.vstamp[pv] = a.stamp
					a.vs[pv].next = noValue
				}
				if a.vs[pv].next != noValue {
					e.bind(pv, int8(en.loc))
					a.varCur[p], a.varDirty[p] = pv, true
				}
			}
		}
	}
	// Pinned parts' entry content lives in the pinned register; uncached
	// parts read their home.
	for _, p := range a.touched {
		pv := a.pseudo(p)
		if a.vs[pv].reg >= 0 && a.regVal[a.vs[pv].reg] == pv {
			continue
		}
		next := a.vs[pv].next
		if a.vstamp[pv] != a.stamp {
			next = noValue
		}
		a.vs[pv] = bVal{next: next, slot: -1, vpart: p, reg: noReg}
		if next == noValue {
			continue
		}
		if r := a.pinReg[p]; r >= 0 {
			a.regVal[r], a.vs[pv].reg = pv, r
			continue
		}
		// The home is the entry content's only copy: a write-back to it
		// first moves the content out if it is still needed.
		a.vs[pv].slot, a.vs[pv].fl = e.varHomeOf(p), vfStored
		a.occupant[p] = pv
	}
}

// fixDefault fixes the entry state of a block no edge has reached yet:
// parameters in registers, everything else in homes.
func (e *baselineEngine) fixDefault(b int32) {
	a := &e.alloc
	blk := &e.blocks[b]
	blk.stateStart = int32(len(a.states))
	taken := uint64(0)
	for j := int32(0); j < blk.nparams; j++ {
		a.states = append(a.states, bEntry{kind: eParam, id: uint32(j), loc: e.pickLocation(e.labelParamBank(b, j), taken)})
		if l := a.states[len(a.states)-1].loc; l < bSlotLoc {
			taken |= regBit(int8(l))
		}
	}
	blk.stateN = int32(len(a.states)) - blk.stateStart
	blk.fixed, blk.deflt = true, true
}

// labelParamBank returns the bank of block b's parameter j: general, unless
// the model keeps 64-bit values in the floating-point bank and the block
// reads the parameter as one.
func (e *baselineEngine) labelParamBank(b, j int32) uint8 {
	if !e.m.wide {
		return bankGP
	}
	blk := &e.blocks[b]
	for i := blk.first; i < blk.end; i++ {
		if r := &e.recs[i]; r.op == ir.OpLabelParam && r.imm == uint64(j) && r.fl&rfDefFP != 0 {
			return bankFP
		}
	}
	return bankGP
}

// pickLocation picks a register of bank not in taken, preferring free ones,
// or a new slot. With a wide model, a general register cannot hold a value of
// the floating-point bank.
func (e *baselineEngine) pickLocation(bank uint8, taken uint64) int16 {
	a := &e.alloc
	mask := e.m.allocatable &^ a.pinned &^ taken
	bankMask := uint64(0xFFFFFFFF)
	if bank == bankFP {
		bankMask <<= 32
	}
	candidates := [...]uint64{a.free & mask & bankMask, mask & bankMask, a.free & mask, mask}
	n := len(candidates)
	if e.m.wide && bank == bankFP {
		n = 2
	}
	for _, m := range candidates[:n] {
		if m != 0 {
			return int16(bits.TrailingZeros64(m))
		}
	}
	return int16(bSlotLoc + e.newSlot())
}

func (e *baselineEngine) newSlot() int32 {
	s := e.alloc.nslots
	e.alloc.nslots++
	return s
}

// fixState fixes block t's entry state at its first arriving edge, from the
// current locations of the edge's arguments, the live-in values and the
// cached Var parts.
func (e *baselineEngine) fixState(t int32, args []uint32) {
	a := &e.alloc
	tb := &e.blocks[t]
	tb.stateStart = int32(len(a.states))
	taken := uint64(0)
	for j, v := range args {
		s := &a.vs[v]
		loc := int16(-1)
		if s.reg >= 0 && taken&regBit(s.reg) == 0 && a.pinned&regBit(s.reg) == 0 {
			loc = int16(s.reg)
		} else {
			bank := bankGP
			if s.reg >= 0 {
				bank = regBank(s.reg)
			} else if e.defFP(v) {
				bank = bankFP
			}
			loc = e.pickLocation(bank, taken)
		}
		if loc < bSlotLoc {
			taken |= regBit(int8(loc))
		}
		a.states = append(a.states, bEntry{kind: eParam, id: uint32(j), loc: loc})
	}
	if a.words != 0 {
		in := a.liveIn[int(t)*a.words : (int(t)+1)*a.words]
		for w, word := range in {
			for word != 0 {
				x := w*64 + bits.TrailingZeros64(word)
				word &= word - 1
				v := a.cross[x]
				if e.recs[v].op == ir.OpConst {
					continue
				}
				s := &a.vs[v]
				if s.reg < 0 {
					continue
				}
				loc := int16(s.reg)
				if taken&regBit(s.reg) != 0 || a.pinned&regBit(s.reg) != 0 {
					// Another entity holds the register: keep the value in
					// a free one rather than reloading it in the block.
					free := a.free & e.m.allocatable &^ a.pinned &^ taken & bankMask(regBank(s.reg))
					if free == 0 {
						continue
					}
					loc = int16(bits.TrailingZeros64(free))
				}
				taken |= regBit(int8(loc))
				a.states = append(a.states, bEntry{kind: eValue, id: v, loc: loc, stored: s.fl&vfStored != 0 && s.fl&vfHome != 0})
			}
		}
	}
	e.forEachEndPart(a.block, func(p int32) {
		if !e.partActive(p, tb.first) {
			return
		}
		c := a.varCur[p]
		if c == noValue {
			return
		}
		s := &a.vs[c]
		if s.reg >= 0 && taken&regBit(s.reg) == 0 && a.pinned&regBit(s.reg) == 0 {
			taken |= regBit(s.reg)
			a.states = append(a.states, bEntry{kind: ePart, id: uint32(p), loc: int16(s.reg)})
		}
	})
	tb.stateN = int32(len(a.states)) - tb.stateStart
	tb.fixed = true
}

// homeOf returns cross-block value v's home slot, assigning it on first
// use. A value is stored to its home lazily: when it is evicted, or on an
// edge into a block that expects it there.
func (e *baselineEngine) homeOf(v uint32) int32 {
	s := &e.alloc.vs[v]
	if s.fl&vfHome == 0 {
		s.slot, s.fl = e.newSlot(), s.fl|vfHome
	}
	return s.slot
}

// varHomeOf returns Var part p's home slot, assigning it on first use.
func (e *baselineEngine) varHomeOf(p int32) int32 {
	a := &e.alloc
	if a.varHome[p] < 0 {
		a.varHome[p] = e.newSlot()
	}
	return a.varHome[p]
}

// poolSlot returns a free block-local spill slot.
func (e *baselineEngine) poolSlot() int32 {
	a := &e.alloc
	if n := len(a.poolFree); n > 0 {
		s := a.poolFree[n-1]
		a.poolFree = a.poolFree[:n-1]
		return s
	}
	s := e.newSlot()
	a.pool = append(a.pool, s)
	return s
}

func (e *baselineEngine) freePoolSlot(s int32) { e.alloc.poolFree = append(e.alloc.poolFree, s) }

func (e *baselineEngine) bind(v uint32, r int8) {
	a := &e.alloc
	a.regVal[r] = v
	a.vs[v].reg = r
	a.free &^= regBit(r)
	a.used |= regBit(r)
}

func (e *baselineEngine) unbind(r int8) {
	a := &e.alloc
	if v := a.regVal[r]; v != noValue {
		a.vs[v].reg = noReg
		a.regVal[r] = noValue
	}
	if a.pinned&regBit(r) == 0 && e.m.allocatable&regBit(r) != 0 {
		a.free |= regBit(r)
	}
}

// release frees a dead value's register and spill slot.
func (e *baselineEngine) release(v uint32) {
	a := &e.alloc
	s := &a.vs[v]
	if r := s.reg; r >= 0 && a.regVal[r] == v {
		a.regVal[r] = noValue
		if a.pinned&regBit(r) == 0 && e.m.allocatable&regBit(r) != 0 {
			a.free |= regBit(r)
		}
	}
	s.reg = noReg
	if s.fl&vfPool != 0 {
		e.freePoolSlot(s.slot)
		s.slot, s.fl = -1, s.fl&^(vfPool|vfStored)
	}
}

// advance moves v to its next use after the current one, releasing it when
// it has none.
func (e *baselineEngine) advance(v, next uint32) {
	e.alloc.vs[v].next = next
	if next == noValue {
		e.release(v)
	}
}

// valueBits returns the width of value v, or of a Var part's entry content
// for a pseudo value. Moves, spills and reloads carry it for architectures
// whose register banks represent narrow values differently.
func (e *baselineEngine) valueBits(v uint32) uint8 {
	if n := uint32(len(e.recs)); v < n {
		return e.recs[v].bits
	} else {
		return e.vars[e.partVar[v-n]].bits
	}
}

// defFP reports whether value v belongs in the floating-point bank: by its
// defining record, or for a pseudo value by its Var part's Sets.
func (e *baselineEngine) defFP(v uint32) bool {
	n := uint32(len(e.recs))
	if v < n {
		return e.recs[v].fl&rfDefFP != 0
	}
	p := v - n
	return e.m.wide && e.vars[e.partVar[p]].bits == 64 || e.alloc.fpVotes[p] > 0
}

func (e *baselineEngine) isConst(v uint32) bool {
	return v < e.alloc.nrec && e.recs[v].op == ir.OpConst
}

// clean reports whether evicting v from its register loses nothing.
func (e *baselineEngine) clean(v uint32) bool {
	a := &e.alloc
	s := &a.vs[v]
	if s.vpart >= 0 && a.varCur[s.vpart] == v && a.varDirty[s.vpart] {
		return false
	}
	return e.isConst(v) || s.fl&vfStored != 0
}

// evict moves the value in register r to memory if it is not already there.
func (e *baselineEngine) evict(r int8) {
	a := &e.alloc
	v := a.regVal[r]
	if v == noValue {
		return
	}
	s := &a.vs[v]
	if s.vpart >= 0 && a.varCur[s.vpart] == v && a.varDirty[s.vpart] {
		e.writeBack(s.vpart)
	}
	if !e.isConst(v) && s.fl&vfStored == 0 {
		if v < a.nrec && a.xid[v] >= 0 {
			e.put(bPhys{k: pkSpill, w: e.valueBits(v), a: r, imm: uint64(e.homeOf(v))})
			s.fl |= vfStored
		} else {
			slot := e.poolSlot()
			e.put(bPhys{k: pkSpill, w: e.valueBits(v), a: r, imm: uint64(slot)})
			s.slot, s.fl = slot, s.fl|vfStored|vfPool
		}
	}
	e.unbind(r)
}

// writeBack stores Var part p's dirty content to its home.
func (e *baselineEngine) writeBack(p int32) {
	a := &e.alloc
	v := a.varCur[p]
	if !a.varDirty[p] || v == noValue {
		return
	}
	home := e.varHomeOf(p)
	e.vacate(p)
	e.moveTo(int16(bSlotLoc+home), v)
	a.varDirty[p] = false
	s := &a.vs[v]
	// A cross-block value keeps its own home: s.slot must not name the Var's
	// home, or homeOf would later reassign it while vfStored still claims
	// the new home holds the value, and an edge would skip storing it.
	cross := v < a.nrec && a.xid[v] >= 0
	if s.fl&vfStored == 0 && !e.isConst(v) && !cross {
		s.slot, s.fl = home, s.fl|vfStored
		a.occupant[p] = v
	}
}

// vacate moves a live value whose only copy is in Var part p's home out of
// it before the home is overwritten.
func (e *baselineEngine) vacate(p int32) {
	a := &e.alloc
	occ := a.occupant[p]
	if occ == noValue || occ == a.varCur[p] {
		// Writing the part's content back leaves the occupant in place:
		// it stays registered, so that a later write of other content
		// still moves it out (an entry value or Set value may outlive its
		// time as the part's content).
		return
	}
	a.occupant[p] = noValue
	home := a.varHome[p]
	s := &a.vs[occ]
	if s.slot != home || s.fl&vfPool != 0 || s.fl&vfHome != 0 {
		return
	}
	if a.vstamp[occ] != a.stamp || s.next == noValue {
		s.slot, s.fl = -1, s.fl&^vfStored
		return
	}
	if s.reg >= 0 {
		s.slot, s.fl = -1, s.fl&^vfStored
		return
	}
	slot := e.poolSlot()
	e.put(bPhys{k: pkReload, a: e.m.transit, imm: uint64(home)})
	e.put(bPhys{k: pkSpill, a: e.m.transit, imm: uint64(slot)})
	s.slot, s.fl = slot, s.fl|vfStored|vfPool
}

const bankMaskGP = uint64(0xFFFFFFFF)

func bankMask(bank uint8) uint64 {
	if bank == bankFP {
		return bankMaskGP << 32
	}
	return bankMaskGP
}

// allocReg returns a register of bank for a new value, preferring hint,
// otherwise a free one, otherwise the occupant used farthest in the future,
// clean values first.
func (e *baselineEngine) allocReg(bank uint8, lock uint64, hint int8, preferSaved bool) int8 {
	if hint < 0 && !preferSaved {
		// The common case: a free caller-saved register.
		if m := e.alloc.free &^ lock & bankMask(bank); m != 0 {
			if c := m &^ e.m.saved; c != 0 {
				return int8(bits.TrailingZeros64(c))
			}
			return int8(bits.TrailingZeros64(m))
		}
	}
	return e.allocRegSlow(bank, lock, hint, preferSaved)
}

func (e *baselineEngine) allocRegSlow(bank uint8, lock uint64, hint int8, preferSaved bool) int8 {
	a := &e.alloc
	if hint >= 0 && regBank(hint) == bank && lock&regBit(hint) == 0 && a.regVal[hint] == noValue &&
		(a.free&regBit(hint) != 0 || a.pinned&regBit(hint) != 0) {
		return hint
	}
	m := a.free &^ lock & bankMask(bank)
	if m != 0 {
		if preferSaved {
			if s := m & e.calleeSavedMask(); s != 0 {
				return int8(bits.TrailingZeros64(s))
			}
		} else if c := m &^ e.calleeSavedMask(); c != 0 {
			return int8(bits.TrailingZeros64(c))
		}
		return int8(bits.TrailingZeros64(m))
	}
	best, bestNext, bestClean := noReg, uint32(0), false
	for m := e.m.allocatable &^ a.pinned &^ lock & bankMask(bank); m != 0; m &= m - 1 {
		r := int8(bits.TrailingZeros64(m))
		v := a.regVal[r]
		if v == noValue {
			return r
		}
		next, clean := a.vs[v].next, e.clean(v)
		if best < 0 || next > bestNext || next == bestNext && clean && !bestClean {
			best, bestNext, bestClean = r, next, clean
		}
	}
	if best < 0 {
		panic("baseline: no register to evict")
	}
	e.evict(best)
	return best
}

func (e *baselineEngine) calleeSavedMask() uint64 { return e.m.saved }

// use returns a register of bank holding v, loading it if needed, and adds
// the register to lock.
func (e *baselineEngine) use(v uint32, bank uint8, lock *uint64) int8 {
	a := &e.alloc
	s := &a.vs[v]
	if s.reg >= 0 {
		if regBank(s.reg) == bank {
			*lock |= regBit(s.reg)
			return s.reg
		}
		t := e.allocReg(bank, *lock|regBit(s.reg), -1, false)
		e.put(bPhys{k: pkMove, w: e.valueBits(v), a: t, b: s.reg})
		a.used |= regBit(t)
		*lock |= regBit(t)
		return t
	}
	r := e.allocReg(bank, *lock, -1, false)
	e.load(r, v)
	e.bind(v, r)
	*lock |= regBit(r)
	return r
}

// load puts v's value in register r without binding it.
func (e *baselineEngine) load(r int8, v uint32) {
	a := &e.alloc
	s := &a.vs[v]
	switch {
	case s.reg >= 0:
		if s.reg != r {
			e.put(bPhys{k: pkMove, w: e.valueBits(v), a: r, b: s.reg})
		}
	case e.isConst(v):
		e.constant(r, v)
	case s.fl&vfStored != 0:
		e.put(bPhys{k: pkReload, w: e.valueBits(v), a: r, imm: uint64(s.slot)})
	default:
		panic(fmt.Sprintf("baseline: value %d has no location", v))
	}
	a.used |= regBit(r)
}

func (e *baselineEngine) constant(r int8, v uint32) {
	c := &e.recs[v]
	e.alloc.used |= regBit(r)
	if r >= fpBase {
		e.alloc.used |= regBit(e.m.transit)
	}
	if c.fl&rfSymbol != 0 {
		e.put(bPhys{k: pkSymbol, w: c.bits, a: r, imm: c.imm})
		return
	}
	p := e.slot()
	p.k, p.w, p.a, p.imm = pkConst, c.bits, r, c.imm
	if r >= fpBase && e.literals {
		// The register's 64 bits are the constant's zero-extended bits.
		p.k, p.w = pkLiteral, 64
	}
	p.sz = uint8(e.size(p))
}

// moveTo copies v to location dst (a register or slot) without changing
// any binding.
func (e *baselineEngine) moveTo(dst int16, v uint32) {
	if dst < bSlotLoc {
		e.load(int8(dst), v)
		return
	}
	a := &e.alloc
	s := &a.vs[v]
	slot := uint64(dst - bSlotLoc)
	switch {
	case s.reg >= 0:
		e.put(bPhys{k: pkSpill, w: e.valueBits(v), a: s.reg, imm: slot})
	case e.isConst(v):
		e.constant(e.m.transit, v)
		e.put(bPhys{k: pkSpill, a: e.m.transit, imm: slot})
	case s.fl&vfStored != 0:
		if int64(s.slot) != int64(slot) {
			e.put(bPhys{k: pkReload, a: e.m.transit, imm: uint64(s.slot)})
			e.put(bPhys{k: pkSpill, a: e.m.transit, imm: slot})
		}
	default:
		panic(fmt.Sprintf("baseline: value %d has no location", v))
	}
}

// def allocates the register for value i defined in bank.
func (e *baselineEngine) def(i uint32, bank uint8, lock uint64) int8 {
	a := &e.alloc
	hint := noReg
	blk := &e.blocks[a.block]
	next := a.vs[i].next
	if next < blk.end {
		// Only a block with Vars has Sets.
		if u := &e.recs[next]; blk.vars && u.op == ir.OpSet && u.a == i {
			if p := e.vars[u.x].part + int32(u.imm); a.pinReg[p] >= 0 && regBank(a.pinReg[p]) == bank {
				hint = a.pinReg[p]
			}
		}
	} else if next == blk.end && blk.term == ir.TermReturn && blk.result == i && bank == bankGP {
		hint = e.m.results[0]
	}
	r := e.allocReg(bank, lock, hint, e.hasCall && e.liveAcrossCall(i))
	e.bind(i, r)
	return r
}

// liveAcrossCall reports whether v's next use lies beyond a call in the
// current block.
func (e *baselineEngine) liveAcrossCall(v uint32) bool {
	a := &e.alloc
	if !e.hasCall {
		return false
	}
	next := a.vs[v].next
	blk := &e.blocks[a.block]
	end := min(next, blk.end)
	for i := v + 1; i < end && i < blk.end; i++ {
		if op := e.recs[i].op; op == ir.OpCallC || op == ir.OpCallGo || op == ir.OpSyscall {
			return true
		}
		if i-v > 64 {
			break
		}
	}
	return false
}

// defined finishes value v's definition: a dead value is released.
func (e *baselineEngine) defined(v uint32) {
	a := &e.alloc
	s := &a.vs[v]
	if s.next == noValue {
		e.release(v)
	}
}

// location returns where v is now, for a move.
func (e *baselineEngine) location(v uint32) int16 {
	a := &e.alloc
	s := &a.vs[v]
	switch {
	case s.reg >= 0:
		return int16(s.reg)
	case e.isConst(v):
		return bConstLoc
	case s.fl&vfStored != 0:
		return int16(bSlotLoc + s.slot)
	}
	panic(fmt.Sprintf("baseline: value %d has no location", v))
}

func (e *baselineEngine) addMove(dst int16, v uint32) {
	src := e.location(v)
	if src == dst {
		return
	}
	e.alloc.moves = append(e.alloc.moves, bMove{dst: dst, src: src, value: v})
}

// resolveMoves emits e.alloc.moves as a sequence: a move waits while its
// destination is another pending move's source, and a cycle is broken
// through a scratch register. Constants are materialized last.
func (e *baselineEngine) resolveMoves() {
	a := &e.alloc
	ms := a.moves
	// Where the cycle register is also an argument register (amd64's R10
	// for system calls), a call's moves may write it; their cycles break
	// through a spill slot instead.
	cycleDst, cycleSlot := false, int32(-1)
	for i := range ms {
		cycleDst = cycleDst || ms[i].dst == int16(e.m.cycle)
	}
	for {
		progress, pending := false, 0
		for i := range ms {
			m := &ms[i]
			if m.dst < 0 || m.src == bConstLoc {
				continue
			}
			blocked := false
			for j := range ms {
				if j != i && ms[j].dst >= 0 && ms[j].src == m.dst {
					blocked = true
					break
				}
			}
			if blocked {
				pending++
				continue
			}
			e.emitMove(m.dst, m.src, e.valueBits(m.value))
			m.dst = -1
			progress = true
		}
		if pending == 0 {
			break
		}
		if !progress {
			for i := range ms {
				if ms[i].dst >= 0 && ms[i].src != bConstLoc {
					d := ms[i].dst
					scratch := int16(e.m.cycle)
					if d < bSlotLoc && regBank(int8(d)) == bankFP {
						scratch = int16(e.m.scratchFP)
					} else if cycleDst {
						if cycleSlot < 0 {
							cycleSlot = e.poolSlot()
						}
						scratch = int16(bSlotLoc + cycleSlot)
					}
					e.emitMove(scratch, d, 0)
					if scratch < bSlotLoc {
						a.used |= regBit(int8(scratch))
					}
					for j := range ms {
						if ms[j].dst >= 0 && ms[j].src == d {
							ms[j].src = scratch
						}
					}
					break
				}
			}
		}
	}
	for i := range ms {
		if m := &ms[i]; m.dst >= 0 && m.src == bConstLoc {
			if m.dst < bSlotLoc {
				e.constant(int8(m.dst), m.value)
				a.used |= regBit(int8(m.dst))
			} else {
				e.constant(e.m.transit, m.value)
				e.put(bPhys{k: pkSpill, a: e.m.transit, imm: uint64(m.dst - bSlotLoc)})
			}
		}
	}
	if cycleSlot >= 0 {
		e.freePoolSlot(cycleSlot)
	}
	a.moves = ms[:0]
}

// emitMove emits one move of a w-bit value (0: the raw register).
func (e *baselineEngine) emitMove(dst, src int16, w uint8) {
	a := &e.alloc
	switch {
	case dst < bSlotLoc && src < bSlotLoc:
		e.put(bPhys{k: pkMove, w: w, a: int8(dst), b: int8(src)})
		a.used |= regBit(int8(dst))
	case dst < bSlotLoc:
		e.put(bPhys{k: pkReload, w: w, a: int8(dst), imm: uint64(src - bSlotLoc)})
		a.used |= regBit(int8(dst))
	case src < bSlotLoc:
		e.put(bPhys{k: pkSpill, w: w, a: int8(src), imm: uint64(dst - bSlotLoc)})
	default:
		e.put(bPhys{k: pkReload, a: e.m.transit, imm: uint64(src - bSlotLoc)})
		e.put(bPhys{k: pkSpill, a: e.m.transit, imm: uint64(dst - bSlotLoc)})
		a.used |= regBit(e.m.transit)
	}
}
