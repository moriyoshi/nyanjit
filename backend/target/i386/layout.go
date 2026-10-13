package i386

import (
	"sync"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
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

// FlagEffect reports whether ins overwrites any arithmetic flag, as
// FlagFacts does.
func FlagEffect(ins machine.Instruction) bool { return FlagFacts(ins).Modified != 0 }

// PhysicalSize measures the same encoding used by both output assemblers.
// goHomes is the number of Go argument homes; 386 passes Go arguments on the
// stack and does not reserve separate register homes, so this count is ignored.
func PhysicalSize(ins machine.Instruction, stack abi.StackPractice, goHomes int) int {
	if ins.Op == machine.OpFloatCompare && ins.Imm >= uint64(len(floatCompareForms)) {
		return 0 // Visit narrows Imm to the condition byte
	}
	if ins.PreserveFlags && ins.ByteOrder == op.ByteOrderNative {
		if f, ok := preservedFor(ins); ok {
			return f.size() // measured without an encoder
		}
	}
	assembler := sizers.Get().(*ByteAssembler)
	assembler.stack = stack
	size := 0
	if err := machine.Visit(ins, assembler); err == nil {
		size = assembler.AssembledSize()
	}
	if cap(assembler.code) <= maxSizerBytes {
		clear(assembler.relocations)
		assembler.Reset()
		sizers.Put(assembler)
	}
	return size
}

// sizers keeps the assemblers PhysicalSize measures with, so measuring an
// instruction allocates neither an assembler nor its code buffer. One that
// grew past maxSizerBytes is dropped rather than kept.
var sizers = sync.Pool{New: func() any { return new(ByteAssembler) }}

const maxSizerBytes = 1 << 12
