package amd64

import (
	"fmt"

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
	base, off := uint8(10), ins.Slot*8
	if stack == abi.StackPlatform {
		base, off = 4, (ins.Slot+homes)*8
	}
	var buf [16]byte
	return len(vectorMemory(buf[:0], 0xf2, 0x10, backend.VectorNumber(ins.A), base, off))
}
func (e *ByteAssembler) FloatMemory(ins machine.Instruction) error {
	if !e.require(ins.Op == machine.OpFloatSpill || ins.Op == machine.OpFloatReload) {
		return e.err
	}
	if !e.require((validInstructionRegisters(ins))) {
		return e.err
	}

	base, off := uint8(10), ins.Slot*8
	if e.stack == abi.StackPlatform {
		base, off = 4, (ins.Slot+e.goHomes)*8
	}
	op := byte(0x11)
	if ins.Op == machine.OpFloatReload {
		op = 0x10
	}
	e.code = vectorMemory(e.code, 0xf2, op, backend.VectorNumber(ins.A), base, off)
	return nil
}
func (e *TextAssembler) FloatMemory(ins machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.FloatMemory(ins) }); err != nil {
		return err
	}
	base, off := uint8(10), ins.Slot*8
	if e.stack == abi.StackPlatform {
		base, off = 4, (ins.Slot+e.goHomes)*8
	}
	dst, src := fmt.Sprintf("[%s + %d]", amd64RegisterNames[base], off), fmt.Sprintf("xmm%d", backend.VectorNumber(ins.A))
	if ins.Op == machine.OpFloatReload {
		dst, src = src, dst
	}
	e.line(FloatMemorySize(ins, e.stack, e.goHomes), "movsd %s, %s", dst, src)
	return nil
}
