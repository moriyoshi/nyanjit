package ppc64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func isCarryBranch(condition ir.BranchCondition) bool {
	return condition >= machine.BranchCarry && condition <= machine.BranchNotCarry32
}

func carryBranchPrefix(cpu abi.PPC64CPU) int64 {
	if cpu >= abi.PPC64Power9 {
		return 4 // mcrxrx cr7
	}
	return 12 // mfxer; srdi; mtcrf
}

// FlagFacts reports exact S/Z/C changes and result facts before value IDs are
// bound by the allocator. CR0.LT/EQ describe sign/zero; XER.CA describes
// full-width carry, and POWER9 XER.CA32 can describe 32-bit carry.
// An untouched flag retains its earlier fact.
func (e *Emitter) FlagFacts(ins machine.Instruction) machine.FlagState {
	var state machine.FlagState
	for _, resource := range e.SpecialWrites(ins) {
		if resource.Class == machine.ResourceConditionField && resource.Number == 0 {
			state.Modified |= machine.FlagSign | machine.FlagZero
		}
		if resource.Class == machine.ResourceIntegerStatus {
			state.Modified |= machine.FlagCarry
		}
	}
	if ins.Op == machine.OpCallC || ins.Op == machine.OpCallGo || ins.Op == machine.OpSyscall {
		state.Modified |= machine.FlagSign | machine.FlagZero | machine.FlagCarry
		return state
	}
	fact := func(width uint8) machine.FlagFact {
		return machine.FlagFact{Valid: true, Width: width, Value: -1, Left: -1, Right: -1}
	}
	width := ins.Bits
	if width == 0 {
		width = 64
	}
	switch ins.Op {
	case machine.OpBinary, machine.OpBinaryImmediate, machine.OpInc, machine.OpDec:
		if state.Modified&machine.FlagZero != 0 {
			// Binary arithmetic produces a machine-width temporary. Bitwise
			// results and AND-immediate are already width-normalized.
			zeroWidth := uint8(64)
			if ins.Arithmetic == ir.OpAnd || ins.Arithmetic == ir.OpOr || ins.Arithmetic == ir.OpXor {
				zeroWidth = width
			}
			state.Zero = fact(zeroWidth)
		}
		if width == 64 && state.Modified&machine.FlagSign != 0 {
			state.Sign = fact(64)
		}
		sub := ins.Arithmetic == ir.OpSub || ins.Op == machine.OpDec
		add := ins.Arithmetic == ir.OpAdd || ins.Op == machine.OpInc
		if state.Modified&machine.FlagCarry != 0 && (sub || add) {
			valid := width == 64
			condition := machine.BranchCarry
			if width == 32 && e.cpu >= abi.PPC64Power9 {
				valid, condition = true, machine.BranchCarry32
			} else if sub && ins.Op == machine.OpBinary {
				// Zero-extended operands have the same unsigned borrow at
				// a narrower width; addition overflow is width-dependent.
				valid = true
			} else if sub && ins.Op == machine.OpBinaryImmediate && ins.Imm > 0 && int64(ins.Imm) > 0 {
				valid = true
			}
			if sub && ins.Op == machine.OpBinaryImmediate && ins.Imm == 0 {
				valid = false // addic 0 sets CA=0, while subtraction has no borrow.
			}
			if valid {
				state.Carry = fact(width)
				state.Carry.NoBorrow = sub
				state.Carry.Condition, state.Carry.ConditionValid = condition, true
			}
		}
	case machine.OpWordCarry:
		state.Sign, state.Zero = fact(64), fact(64)
		// Carry also depends on the incoming bit, so two-operand facts do not apply.
	case machine.OpNarrow:
		if ins.FlagRequest != 0 && ins.Imm < 64 {
			state.Zero = fact(uint8(ins.Imm))
		}
	case machine.OpSignExtend:
		if ins.FlagRequest != 0 {
			state.Sign, state.Zero = fact(64), fact(64)
		}
	}
	return state
}
