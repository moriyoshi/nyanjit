package arm64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
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
		// The flag-free forms dead-flag elision selects write no NZCV.
		if ins.PreserveFlags {
			return s
		}
		operation := ins.Arithmetic
		if ins.Op == machine.OpDec {
			operation = op.OpSub
		}
		if operation != op.OpAdd && operation != op.OpSub && operation != op.OpAnd {
			return s
		}
		if ins.Op == machine.OpBinaryImmediate && operation == op.OpAnd {
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
		if ins.Op == machine.OpBinaryImmediate && operation == op.OpSub && width < actual {
			encodedOp, _, _, ok := addSubImmediate(operation, width, ins.Imm)
			// A negative narrow immediate becomes an ADD in the 32-bit
			// datapath. Its carry is not the narrow subtraction's no-borrow.
			flippedNarrowSub = ok && encodedOp != operation
		}
		if operation == op.OpSub && !flippedNarrowSub || operation == op.OpAdd && actual == width {
			s.Carry = fact(width)
			s.Carry.NoBorrow = operation == op.OpSub
			s.Carry.Condition = op.BranchAboveEqual
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
		if ins.Arithmetic == op.OpTrailingZeros && ins.Bits < 32 {
			s.Modified = all
		}
	case machine.OpDivide:
		// Only the all-ones quotient compares the divisor.
		if divisionWritesFlags(ins) {
			s.Modified = all
		}
	case machine.OpShiftBy:
		if ins.Arithmetic != op.OpRotateLeft && ins.Arithmetic != op.OpRotateRight {
			s.Modified = all
		}
	case machine.OpSelect, machine.OpCallC, machine.OpCallGo, machine.OpSyscall, machine.OpCompareImmediate, machine.OpCompareRegister, machine.OpFloatCompare:
		s.Modified = all
	}
	return s
}
