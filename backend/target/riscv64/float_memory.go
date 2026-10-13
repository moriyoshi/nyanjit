package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func FloatMemorySize(ins machine.Instruction, stack abi.StackPractice, homes int) int {
	if ins.Op != machine.OpFloatSpill && ins.Op != machine.OpFloatReload {
		return 0
	}
	if !validInstructionRegisters(ins) || !validSlot(ins.Slot, homes) {
		return 0
	}
	a := NewByteAssembler(stack, homes)
	a.FloatMemory(ins)
	return len(a.words) * 4
}
func (e *ByteAssembler) FloatMemory(ins machine.Instruction) error {
	if !e.require(ins.Op == machine.OpFloatSpill || ins.Op == machine.OpFloatReload) {
		return e.err
	}
	if !e.require((validInstructionRegisters(ins))) {
		return e.err
	}

	base, off := backend.Register(9), ins.Slot*8
	if e.stack == abi.StackPlatform {
		base, off = 2, (ins.Slot+1+e.goHomes)*8
	}
	r, d := e.memoryBase(base, int64(off))
	n := uint32(backend.VectorNumber(ins.A))
	if ins.Op == machine.OpFloatReload {
		e.words = append(e.words, iWord(d, uint32(r), 3, n, 0x07))
	} else {
		e.words = append(e.words, sWord(d, n, uint32(r), 3)&^uint32(0x7f)|0x27)
	}
	return nil
}
