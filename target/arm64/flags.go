package arm64

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// FlagFacts records S/Z/C from S-form arithmetic. Subtraction's C means no
// borrow, while addition's C means carry. Ordinary moves and truncations
// preserve NZCV, and are aliased only when the proven widths agree.
func FlagFacts(ins machine.Instruction) machine.FlagState {
	var s machine.FlagState
	all := machine.FlagSign | machine.FlagZero | machine.FlagCarry
	fact := func(w uint8) machine.FlagFact {
		return machine.FlagFact{Valid: true, Width: w, Value: -1, Left: -1, Right: -1}
	}
	width := ins.Bits
	if width == 0 {
		width = 64
	}
	actual := width
	if actual < 32 {
		actual = 32
	}
	switch ins.Op {
	case machine.OpBinary, machine.OpBinaryImmediate, machine.OpDec:
		if ins.Op == machine.OpDec && ins.PreserveFlags {
			return s
		}
		op := ins.Arithmetic
		if ins.Op == machine.OpDec {
			op = ir.OpSub
		}
		if op != ir.OpAdd && op != ir.OpSub && op != ir.OpAnd {
			return s
		}
		if ins.Op == machine.OpBinaryImmediate && op == ir.OpAnd {
			if _, encodedWidth, ok := logicalImmediatePlan(width, ins.Imm); ok {
				actual = encodedWidth
			}
		}
		s.Modified = all
		s.Zero, s.Sign = fact(actual), fact(actual)
		// Normalization after the arithmetic cannot change the CPU flags.
		// Sign flags from a wider datapath do not describe a narrow result.
		if actual != width {
			s.Sign = machine.FlagFact{}
		}

		flippedNarrowSub := false
		if ins.Op == machine.OpBinaryImmediate && op == ir.OpSub && width < actual {
			encodedOp, _, _, ok := addSubImmediate(op, width, ins.Imm)
			// A negative narrow immediate becomes an ADD in the 32-bit
			// datapath. Its carry is not the narrow subtraction's no-borrow.
			flippedNarrowSub = ok && encodedOp != op
		}
		if op == ir.OpSub && !flippedNarrowSub || op == ir.OpAdd && actual == width {
			s.Carry = fact(width)
			s.Carry.NoBorrow = op == ir.OpSub
			s.Carry.Condition = ir.BranchAboveEqual
			s.Carry.ConditionValid = true
		}
	case machine.OpWordCarry:
		s.Modified = all
		s.Sign, s.Zero = fact(actual), fact(actual)
	case machine.OpCarryBit:
		if ins.CarryMode != machine.CarryFlags {
			s.Modified = all
		}
	case machine.OpCountZeros:
		if ins.Arithmetic == ir.OpTrailingZeros && ins.Bits < 32 {
			s.Modified = all
		}
	case machine.OpShiftBy:
		if ins.Arithmetic != ir.OpRotateLeft && ins.Arithmetic != ir.OpRotateRight {
			s.Modified = all
		}
	case machine.OpSelect, machine.OpCallC, machine.OpCallGo, machine.OpSyscall, machine.OpCompareImmediate, machine.OpCompareRegister:
		s.Modified = all
	}
	return s
}
