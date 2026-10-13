package compile

import (
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

// bFlagSelect in an OpSelect record's imm says it selects from the flags of
// the comparison its condition is, by the condition in the low byte.
const bFlagSelect = 1 << 8

// bBigEndian in an OpLoad64 or OpStore64 record's imm says the access is
// big-endian: the target reverses the bytes it loads or stores.
const bBigEndian = 1 << 9

// bigEndian64 returns the imm marking a 64-bit access of record r.
func bigEndian64(r *bRec) uint64 {
	if (r.op == ir.OpLoad64 || r.op == ir.OpStore64) && op.ByteOrder(r.ord) == op.ByteOrderBigEndian {
		return bBigEndian
	}
	return 0
}

// sizedBigEndian reports a big-endian 64-bit access, which fast size
// functions leave to measurement.
func sizedBigEndian(p *bPhys) bool {
	return p.imm == bBigEndian && (ir.Opcode(p.sub) == ir.OpLoad64 || ir.Opcode(p.sub) == ir.OpStore64)
}

// baselineCompareFlags is implemented by an architecture whose OpCompare
// code leaves flags that decide the comparison and whose OpSelect can read
// them.
type baselineCompareFlags interface {
	// compareFlags returns the condition that holds after the code of
	// OpCompare record r, lowered with dst and src, exactly when its
	// result is 1.
	compareFlags(e *baselineEngine, r *bRec, dst int8, src [3]int8) (op.BranchCondition, bool)
	// flagsKept reports whether records keep the flags.
	flagsKept(records []bPhys) bool
}

// noteCompare records OpCompare record i, just lowered, as the flags' source
// when its target's code leaves flags that decide it.
func (e *baselineEngine) noteCompare(i uint32, r *bRec, dst int8, src [3]int8) {
	a := &e.alloc
	a.cmp = -1
	cf, ok := e.arch.(baselineCompareFlags)
	if !ok {
		return
	}
	if c, ok := cf.compareFlags(e, r, dst, src); ok {
		a.cmp, a.cmpBlock, a.cmpAt, a.cmpCond = int32(i), a.block, int32(len(a.main)), c
	}
}

// fusedCompare returns the condition that decides value v from the live
// flags: v is the last OpCompare of the block and nothing since has changed
// the flags.
func (e *baselineEngine) fusedCompare(v uint32) (op.BranchCondition, bool) {
	a := &e.alloc
	if a.cmp < 0 || v != uint32(a.cmp) || a.cmpBlock != a.block || a.out != &a.main {
		return 0, false
	}
	if !e.arch.(baselineCompareFlags).flagsKept(a.main[a.cmpAt:]) {
		return 0, false
	}
	return a.cmpCond, true
}

// fusedCompareBranch returns the condition that decides predicate leaf n,
// a test of an OpCompare value against zero, from the flags of that compare.
func (e *baselineEngine) fusedCompareBranch(n *bPred) (ir.BranchCondition, bool) {
	if e.options.Harden || n.withValue || n.imm != 0 || n.cond != ir.BranchNotEqual && n.cond != ir.BranchEqual {
		return 0, false
	}
	c, ok := e.fusedCompare(n.value)
	if ok && n.cond == ir.BranchEqual {
		c ^= 1
	}
	return c, ok
}
