package compile

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// stepOps returns the instructions of a builder step, which its predicate's
// values index.
func stepOps(step emissionStep) []instruction {
	switch {
	case step.segment != nil:
		return step.segment.ops
	case step.fragment != nil:
		return step.fragment.Instructions
	}
	return nil
}

// overflowShape reports whether comparison node is Builder.Overflow of the
// OpAdd, OpSub or OpMul at its anchor, at a width whose flags the target
// encodes: 32 or 64 bits on amd64, arm64 and 386 (where the OF of a pair's
// high-word ADC or SBB is that of the 64-bit result), and a product of the
// register width on amd64 (64 bits) and 386 (32 bits). It returns the flag
// condition that holds when node does. The shape is checked against the
// anchor's operands, so an anchor never changes what a predicate computes.
func overflowShape(node *ir.PredicateExpr, ops []instruction, architecture string) (ir.BranchCondition, bool) {
	switch architecture {
	case "amd64", "386", "arm64":
	default:
		return 0, false
	}
	if node == nil || node.Kind != ir.PredicateCompare || !node.HasAnchor || node.Anchor < 0 || node.Anchor >= len(ops) {
		return 0, false
	}
	s := node.Anchor
	arith := ops[s]
	w := instructionBits(arith)
	if w != 64 && w != 32 {
		return 0, false
	}
	at := func(id int) (instruction, bool) {
		if id < 0 || id >= len(ops) {
			return instruction{}, false
		}
		return ops[id], true
	}
	pair := func(i instruction, x, y int) bool { return i.A == x && i.B == y || i.A == y && i.B == x }
	yes := machine.BranchOverflow
	switch arith.Op {
	case opAdd, opSub:
		if node.WithValue || node.Immediate != 0 || node.Condition != ir.BranchLess && node.Condition != ir.BranchGreaterEqual {
			return 0, false
		}
		if node.Condition == ir.BranchGreaterEqual {
			yes = machine.BranchNotOverflow
		}
		// IsNegative of (a^s)&(b^s) for a sum, (a^b)&(a^s) for a difference;
		// narrower than 64 bits it reads the sign extension.
		v := node.Value
		if extend, ok := at(v); ok && extend.Op == ir.OpSignExtend && extend.Imm == uint64(w) && w < 64 {
			v = extend.A
		}
		and, ok := at(v)
		if !ok || and.Op != opAnd || instructionBits(and) != w {
			return 0, false
		}
		x, okx := at(and.A)
		y, oky := at(and.B)
		if !okx || !oky || x.Op != opXor || y.Op != opXor {
			return 0, false
		}
		if arith.Op == opAdd && !(pair(x, arith.A, s) && pair(y, arith.B, s) || pair(x, arith.B, s) && pair(y, arith.A, s)) ||
			arith.Op == opSub && !(pair(x, arith.A, arith.B) && pair(y, arith.A, s) || pair(y, arith.A, arith.B) && pair(x, arith.A, s)) {
			return 0, false
		}
	case opMul:
		// MulHighSigned(a, b) != p>>(w-1), arithmetic: IMUL's CF and OF.
		if !(architecture == "amd64" && w == 64 || architecture == "386" && w == 32) || !node.WithValue || node.Condition != ir.BranchNotEqual && node.Condition != ir.BranchEqual {
			return 0, false
		}
		if node.Condition == ir.BranchEqual {
			yes = machine.BranchNotOverflow
		}
		high, okh := at(node.Value)
		sign, oks := at(node.Other)
		if !okh || !oks || high.Op != ir.OpMulHighSigned || !pair(high, arith.A, arith.B) || instructionBits(high) != w {
			return 0, false
		}
		// The product's sign repeated: p>>(w-1), arithmetic, at 64 bits, and
		// 0 - (p >> (w-1)) narrower, as Builder.Overflow forms them.
		repeated := sign.Op == ir.OpShiftRightArithmetic && sign.A == s && sign.Imm == uint64(w-1)
		if w < 64 && sign.Op == opSub && instructionBits(sign) == w {
			zero, okz := at(sign.A)
			shifted, okt := at(sign.B)
			repeated = okz && okt && zero.Op == ir.OpConst && zero.Symbol == "" && zero.Imm == 0 &&
				shifted.Op == ir.OpShiftRight && instructionBits(shifted) == w && shifted.A == s && shifted.Imm == uint64(w-1)
		}
		if !repeated {
			return 0, false
		}
	default:
		return 0, false
	}
	return yes, true
}

// overflowAnchor returns the anchor of predicate p when every comparison in
// it is an overflow test of that one anchor: a branch block can then keep the
// anchor as its result, so that its flags are the last written.
func overflowAnchor(p *ir.PredicateExpr, ops []instruction, architecture string) (int, bool) {
	anchor := -1
	for comparison, err := range predicateComparisons(p) {
		if err != nil {
			return 0, false
		}
		if _, ok := overflowShape(comparison, ops, architecture); !ok || anchor >= 0 && comparison.Anchor != anchor {
			return 0, false
		}
		anchor = comparison.Anchor
	}
	return anchor, anchor >= 0
}

// overflowFlagPlan decides an overflow predicate (Builder.Overflow) from
// the overflow flag of its anchor when that arithmetic was the last to write
// the flags: x86 OF (JO/JNO) after ADD, SUB, a 386 pair's ADC or SBB and the
// register-width IMUL, and arm64 V (B.VS/B.VC) after ADDS and SUBS.
func overflowFlagPlan(node *ir.PredicateExpr, ops []instruction, flags flagState, architecture string) (flagBranchPlan, bool) {
	yes, ok := overflowShape(node, ops, architecture)
	if !ok {
		return flagBranchPlan{}, false
	}
	s := node.Anchor
	arith := ops[s]
	w := instructionBits(arith)
	widthMatches := func(fact machine.FlagFact) bool { return fact.Width == 0 || fact.Width == w }
	if arith.Op == opMul {
		// IMUL leaves S and Z undefined; a later INC or DEC would set them
		// and change OF while keeping CF.
		if !flags.Carry.MatchesValue(s) || flags.Carry.Operation != opMul || flags.Carry.Width != w || flags.Sign.Valid || flags.Zero.Valid {
			return flagBranchPlan{}, false
		}
	} else if architecture == "386" && w == 64 {
		// A pair has no zero fact; its sign and carry name the anchor
		// instead, and every 386 writer of OF writes S or C too.
		if !flags.Sign.MatchesValue(s) || !flags.Carry.MatchesValue(s) || flags.Carry.Operation != arith.Op || flags.Sign.Width != w || flags.Carry.Width != w {
			return flagBranchPlan{}, false
		}
	} else if !flags.Zero.MatchesValue(s) || !flags.Carry.MatchesValue(s) ||
		flags.Carry.Operation != arith.Op || !widthMatches(flags.Zero) || !widthMatches(flags.Carry) {
		// The zero and carry facts name the anchor: no later instruction
		// wrote a flag (each writer of OF or V writes Z or C too), so OF or
		// V is the anchor's. The sign fact may be gone: it describes the
		// register image, which narrow arithmetic does not set.
		return flagBranchPlan{}, false
	}
	return flagBranchPlan{yes: []ir.BranchCondition{yes}, valid: true}, true
}
