package amd64

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// FlagFacts distinguishes INC/DEC's preserved CF from written S/Z. Widths
// describe the instruction actually encoded, including 32-bit immediates.
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
	switch ins.Op {
	case machine.OpBinary, machine.OpBinaryImmediate, machine.OpInc, machine.OpDec:
		if ins.PreserveFlags {
			return s // LEA
		}
		s.Modified = all
		if ins.Op == machine.OpInc || ins.Op == machine.OpDec {
			s.Modified &^= machine.FlagCarry
		}
		if ins.Arithmetic == op.OpMul {
			return s
		} // IMUL leaves S/Z undefined.
		actual := width
		if ins.Op == machine.OpBinaryImmediate && width < 32 {
			actual = 32
		}
		s.Sign, s.Zero = fact(actual), fact(actual)
		if s.Modified&machine.FlagCarry != 0 && (ins.Arithmetic == op.OpAdd && width == actual || ins.Arithmetic == op.OpSub) {
			s.Carry = fact(width)
			s.Carry.Condition = op.BranchBelow
			s.Carry.ConditionValid = true
		}
	case machine.OpWordCarry:
		s.Modified = all
		s.Sign, s.Zero = fact(width), fact(width)
	case machine.OpCarryBit:
		if ins.CarryMode != machine.CarryFlags {
			s.Modified = all
		}
	case machine.OpShift:
		if ins.Imm != 0 {
			s.Modified = all
		} // Counts can leave carry undefined; no result fact.
	case machine.OpIntToFloat, machine.OpFloatToInt:
		if convertWritesFlags(ins) {
			s.Modified = all
		}
	case machine.OpCountZeros, machine.OpShiftBy, machine.OpMulHigh, machine.OpMulHighSigned, machine.OpDivide, machine.OpSelect, machine.OpCallC, machine.OpCallGo, machine.OpSyscall, machine.OpFrameEnter, machine.OpFrameLeave, machine.OpCompareImmediate, machine.OpCompareRegister, machine.OpFloatCompare:
		s.Modified = all
	}
	return s
}

// FlagFactsWithCPU accounts for optional instructions that preserve flags.
func FlagFactsWithCPU(ins machine.Instruction, p abi.ResolvedCPU) machine.FlagState {
	if ins.Op == machine.OpMulHigh && ins.Bits == 64 && p.Has("bmi2") {
		return machine.FlagState{}
	}
	if ins.Op == machine.OpShift && canRORX(ins.Arithmetic, ins.Bits, p) {
		return machine.FlagState{}
	}
	if (ins.Op == machine.OpShift || ins.Op == machine.OpShiftBy) && (ins.Arithmetic == op.OpRotateLeft || ins.Arithmetic == op.OpRotateRight) {
		if ins.Op == machine.OpShift && ins.Imm == 0 {
			return machine.FlagState{}
		}
		return machine.FlagState{Modified: machine.FlagCarry}
	}
	return FlagFacts(ins)
}
