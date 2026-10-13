package compile

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// emitPairedPredicate compares high words first, then unsigned low words.
// Both operands have been staged by the fragment. ECX is borrowed only during
// a compare and restored before any branch, preserving its home and the flags.
func emitPairedPredicate(s *physicalEmitter, p *ir.PredicateExpr, slots map[int]int, save int, yes, no, fallthroughLabel Label, written map[physReg]bool) error {
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
	compare := func(word int, condition BranchCondition) {
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
	jump := func(c BranchCondition, l Label) { s.append(physicalInsn{Op: pJumpIf, Condition: c, Imm: uint64(l)}) }
	c := p.Condition
	compare(1, c)
	switch c {
	case BranchEqual, BranchNotEqual:
		mismatch := no
		if c == BranchNotEqual {
			mismatch = yes
		}
		jump(BranchNotEqual, mismatch)
		compare(0, c)
		jump(c, yes)
	case BranchLess, BranchGreaterEqual, BranchLessEqual, BranchGreater, BranchBelow, BranchAboveEqual, BranchBelowEqual, BranchAbove:
		less, greater := BranchBelow, BranchAbove
		if c >= BranchLess && c <= BranchGreater {
			less, greater = BranchLess, BranchGreater
		}
		belowYes := c == BranchLess || c == BranchLessEqual || c == BranchBelow || c == BranchBelowEqual
		lower, upper := no, yes
		if belowYes {
			lower, upper = yes, no
		}
		jump(less, lower)
		jump(greater, upper)
		unsigned := c
		if c >= BranchLess && c <= BranchGreater {
			unsigned += BranchBelow - BranchLess
		}
		compare(0, unsigned)
		jump(unsigned, yes)
	default:
		return fmt.Errorf("386 paired predicate condition %d: %w", c, ErrBackendLimit)
	}
	if no != fallthroughLabel {
		s.append(physicalInsn{Op: pJump, Imm: uint64(no)})
	}
	written[0] = true
	return nil
}

func predicateNeedsPairedWords(p *ir.PredicateExpr, ops []instruction) bool {
	if p == nil {
		return false
	}
	if p.Kind == ir.PredicateCompare && p.Value >= 0 && p.Value < len(ops) && instructionBits(ops[p.Value]) > 32 {
		return true
	}
	return predicateNeedsPairedWords(p.Left, ops) || predicateNeedsPairedWords(p.Right, ops)
}
