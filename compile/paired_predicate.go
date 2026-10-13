package compile

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// emitPairedPredicate compares high words first, then unsigned low words.
// Both operands have been staged by the fragment. ECX is borrowed only during
// a compare and restored before any branch, preserving its home and the flags.
func emitPairedPredicate(s *physicalEmitter, p *ir.PredicateExpr, slots map[int]int, save int, yes, no, fallthroughLabel ir.Label, written *registerWrites) error {
	left, ok := slots[p.Value]
	if !ok {
		return fmt.Errorf("386 paired predicate missing value %d: %w", p.Value, ErrInvalidFunction)
	}
	right := -1
	if p.WithValue {
		right, ok = slots[p.Other]
		if !ok {
			return fmt.Errorf("386 paired predicate missing value %d: %w", p.Other, ErrInvalidFunction)
		}
	}
	compare := func(word int, condition ir.BranchCondition) {
		s.append(physicalInsn{Op: pReload, A: 0, Slot: left + word})
		if right >= 0 {
			s.append(physicalInsn{Op: pSpill, A: 1, Slot: save})
			s.append(physicalInsn{Op: pReload, A: 1, Slot: right + word})
			s.append(physicalInsn{Op: pCompareRegister, Bits: 32, Comparison: machine.ComparisonFor(condition), A: 0, B: 1})
			s.append(physicalInsn{Op: pReload, A: 1, Slot: save})
		} else {
			imm := uint64(p.Immediate) >> (32 * word)
			s.append(physicalInsn{Op: pCompareImmediate, Bits: 32, Comparison: machine.ComparisonFor(condition), A: 0, Imm: uint64(uint32(imm))})
		}
	}
	if err := emitPairedBranches(s, p, compare, yes, no, fallthroughLabel); err != nil {
		return err
	}
	written[0] = true
	return nil
}

// emitPairedRegisters compares the pairs Online left p's values in, high
// words first, then unsigned low words.
func emitPairedRegisters(s *physicalEmitter, p *ir.PredicateExpr, pairs map[int][2]physReg, yes, no, fallthroughLabel ir.Label) error {
	left, ok := pairs[p.Value]
	if !ok {
		return fmt.Errorf("386 paired predicate missing value %d: %w", p.Value, ErrInvalidFunction)
	}
	var right [2]physReg
	if p.WithValue {
		if right, ok = pairs[p.Other]; !ok {
			return fmt.Errorf("386 paired predicate missing value %d: %w", p.Other, ErrInvalidFunction)
		}
	}
	compare := func(word int, condition ir.BranchCondition) {
		if p.WithValue {
			s.append(physicalInsn{Op: pCompareRegister, Bits: 32, Comparison: machine.ComparisonFor(condition), A: left[word], B: right[word]})
		} else {
			imm := uint64(p.Immediate) >> (32 * word)
			s.append(physicalInsn{Op: pCompareImmediate, Bits: 32, Comparison: machine.ComparisonFor(condition), A: left[word], Imm: uint64(uint32(imm))})
		}
	}
	return emitPairedBranches(s, p, compare, yes, no, fallthroughLabel)
}

// emitPairedBranches branches on p with compare, which compares one word of
// its operands: the high words decide unless equal, then the low words.
func emitPairedBranches(s *physicalEmitter, p *ir.PredicateExpr, compare func(int, ir.BranchCondition), yes, no, fallthroughLabel ir.Label) error {
	jump := func(c ir.BranchCondition, l ir.Label) {
		s.append(physicalInsn{Op: pJumpIf, Condition: c, Imm: uint64(l)})
	}
	c := p.Condition
	compare(1, c)
	switch c {
	case ir.BranchEqual, ir.BranchNotEqual:
		mismatch := no
		if c == ir.BranchNotEqual {
			mismatch = yes
		}
		jump(ir.BranchNotEqual, mismatch)
		compare(0, c)
		jump(c, yes)
	case ir.BranchLess, ir.BranchGreaterEqual, ir.BranchLessEqual, ir.BranchGreater, ir.BranchBelow, ir.BranchAboveEqual, ir.BranchBelowEqual, ir.BranchAbove:
		less, greater := ir.BranchBelow, ir.BranchAbove
		if c >= ir.BranchLess && c <= ir.BranchGreater {
			less, greater = ir.BranchLess, ir.BranchGreater
		}
		belowYes := c == ir.BranchLess || c == ir.BranchLessEqual || c == ir.BranchBelow || c == ir.BranchBelowEqual
		lower, upper := no, yes
		if belowYes {
			lower, upper = yes, no
		}
		jump(less, lower)
		jump(greater, upper)
		unsigned := c
		if c >= ir.BranchLess && c <= ir.BranchGreater {
			unsigned += ir.BranchBelow - ir.BranchLess
		}
		compare(0, unsigned)
		jump(unsigned, yes)
	default:
		return fmt.Errorf("386 paired predicate condition %d: %w", c, ErrBackendLimit)
	}
	if no != fallthroughLabel {
		s.append(physicalInsn{Op: pJump, Imm: uint64(no)})
	}
	return nil
}

func predicateNeedsPairedWords(p *ir.PredicateExpr, ops []instruction) bool {
	for node, err := range predicateComparisons(p) {
		if err != nil {
			return false
		}
		if node.Value >= 0 && node.Value < len(ops) && instructionBits(ops[node.Value]) > 32 {
			return true
		}
	}
	return false
}
