package compile

import "github.com/moriyoshi/nyanjit/ir"

// terminateSwitch lowers block b's TermSwitch to a compare tree, as the
// optimized tier does (switch_lower.go): one unsigned bounds comparison sends
// the selector to the default, then a binary search on the bounds of the
// coalesced case ranges picks the destination. Every destination edge goes
// through edge: the first arriving edge fixes the block's entry state, and
// an edge with moves goes through a stub that holds them. Each fallthrough
// of a comparison starts a label of its own, so that a hardened function
// has a barrier on both of its paths.
func (e *baselineEngine) terminateSwitch(b int32) {
	a := &e.alloc
	blk := &e.blocks[b]
	args := e.args[blk.argStart : blk.argStart+blk.nArgs]
	if cap(e.caseDest) < len(e.blocks) {
		e.caseDest = make([]int32, len(e.blocks))
	}
	dest := e.caseDest[:len(e.blocks)]
	for _, t := range e.cases[blk.succStart : blk.succStart+blk.nSucc] {
		e.edge(b, t, args)
		dest[t] = e.blocks[t].label
		if len(a.moves) != 0 {
			stub := e.newLabel()
			main := a.out
			a.out = &a.stubs
			e.put(bPhys{k: pkLabel, imm: uint64(stub)})
			e.resolveMoves()
			e.put(bPhys{k: pkJump, imm: uint64(dest[t])})
			a.out = main
			dest[t] = stub
		}
	}
	next := int32(-1)
	if a.next >= 0 {
		next = e.blocks[a.next].label
	}
	selector := blk.result
	bits := e.valueBits(selector)
	cases := e.cases[blk.caseStart : blk.caseStart+blk.nCases]
	e.ranges = switchRanges(e.ranges[:0], cases, bits)
	for i := range e.ranges {
		e.ranges[i].label = int(dest[e.ranges[i].label])
	}
	compare := func(cond ir.BranchCondition, k uint64, t, f, next int32) {
		e.branchCompare(&bPred{kind: ir.PredicateCompare, cond: cond, value: selector, other: noValue, imm: int64(k)}, t, f, next)
	}
	if switchHasDefault(len(cases), bits) {
		if len(e.ranges) == 1 {
			compare(ir.BranchAboveEqual, uint64(len(cases)), dest[blk.to], int32(e.ranges[0].label), next)
		} else {
			fall := e.newLabel()
			compare(ir.BranchAboveEqual, uint64(len(cases)), dest[blk.to], fall, fall)
			e.put(bPhys{k: pkLabel, imm: uint64(fall)})
			e.switchTree(compare, 0, len(e.ranges)-1, next)
		}
	} else {
		e.switchTree(compare, 0, len(e.ranges)-1, next)
	}
	a.flags = true
}

// switchTree emits the search of e.ranges[lo..hi], whose labels are edge
// labels; next is the label that follows the code.
func (e *baselineEngine) switchTree(compare func(ir.BranchCondition, uint64, int32, int32, int32), lo, hi int, next int32) {
	if lo == hi {
		if t := int32(e.ranges[lo].label); t != next {
			e.put(bPhys{k: pkJump, imm: uint64(t)})
		}
		return
	}
	mid := (lo + hi + 1) / 2
	bound := e.ranges[mid].start
	left, right := int32(e.ranges[lo].label), int32(e.ranges[hi].label)
	switch {
	case lo == mid-1 && mid == hi:
		compare(ir.BranchBelow, bound, left, right, next)
	case lo == mid-1:
		fall := e.newLabel()
		compare(ir.BranchBelow, bound, left, fall, fall)
		e.put(bPhys{k: pkLabel, imm: uint64(fall)})
		e.switchTree(compare, mid, hi, next)
	case mid == hi:
		fall := e.newLabel()
		compare(ir.BranchBelow, bound, fall, right, fall)
		e.put(bPhys{k: pkLabel, imm: uint64(fall)})
		e.switchTree(compare, lo, mid-1, next)
	default:
		lower, upper := e.newLabel(), e.newLabel()
		compare(ir.BranchBelow, bound, lower, upper, upper)
		e.put(bPhys{k: pkLabel, imm: uint64(upper)})
		e.switchTree(compare, mid, hi, lower)
		e.put(bPhys{k: pkLabel, imm: uint64(lower)})
		e.switchTree(compare, lo, mid-1, next)
	}
}
