package i386

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// FrameBytes reserves the callee-saved register pushes and aligned spill area.
func FrameBytes(slots int, hasCall bool) int {
	return FrameBytesForCallArea(slots, hasCall, 0)
}

// FrameBytesForCallArea includes register saves, spills, and the real outgoing
// argument/result byte count. With calls, ESP is aligned to 16 bytes before CALL.
func FrameBytesForCallArea(slots int, hasCall bool, callArea int) int {
	if hasCall {
		return 12 + ((slots*4 + callArea + 4 + 15) &^ 15)
	}
	return (slots*4 + 16 + 15) &^ 15
}

func FlagEffect(ins machine.Instruction) bool {
	switch ins.Op {
	case machine.OpMulHigh, machine.OpMulHighSigned, machine.OpCarryBit, machine.OpWordCarry, machine.OpShiftBy, machine.OpBinary, machine.OpBinaryImmediate, machine.OpBinaryCarry, machine.OpMulWide, machine.OpInc, machine.OpDec, machine.OpNarrow,
		machine.OpSignExtend, machine.OpShift, machine.OpSelect,
		machine.OpCompareImmediate, machine.OpCompareRegister,
		machine.OpFrameEnter, machine.OpFrameLeave,
		machine.OpDivide, machine.OpIntToFloat, machine.OpFloatToInt, machine.OpFloatCompare:
		return true
	}
	return false
}

// PhysicalSize measures the same encoding used by both output assemblers.
// goHomes is the number of Go argument homes; 386 passes Go arguments on the
// stack and does not reserve separate register homes, so this count is ignored.
func PhysicalSize(ins machine.Instruction, stack abi.StackPractice, goHomes int) int {
	if ins.Op == machine.OpFloatCompare && ins.Imm >= uint64(len(floatCompareForms)) {
		return 0 // Visit narrows Imm to the condition byte
	}
	assembler := NewByteAssembler(stack)
	if err := machine.Visit(ins, assembler); err != nil {
		return 0
	}
	return assembler.AssembledSize()
}
