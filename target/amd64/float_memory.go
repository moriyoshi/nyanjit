package amd64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/machine"
)

func FloatMemorySize(ins machine.Instruction, stack abi.StackPractice, homes int) int {
	base, off := uint8(10), ins.Slot*8
	if stack == abi.StackPlatform {
		base, off = 4, (ins.Slot+homes)*8
	}
	return len(vectorMemory(0xf2, 0x10, machine.VectorNumber(ins.A), base, off))
}
func (e *ByteAssembler) FloatMemory(ins machine.Instruction) error {
	base, off := uint8(10), ins.Slot*8
	if e.stack == abi.StackPlatform {
		base, off = 4, (ins.Slot+e.goHomes)*8
	}
	op := byte(0x11)
	if ins.Op == machine.OpFloatReload {
		op = 0x10
	}
	e.code = append(e.code, vectorMemory(0xf2, op, machine.VectorNumber(ins.A), base, off)...)
	return nil
}
func (e *TextAssembler) FloatMemory(ins machine.Instruction) error {
	base, off := uint8(10), ins.Slot*8
	if e.stack == abi.StackPlatform {
		base, off = 4, (ins.Slot+e.goHomes)*8
	}
	dst, src := fmt.Sprintf("[%s + %d]", amd64RegisterNames[base], off), fmt.Sprintf("xmm%d", machine.VectorNumber(ins.A))
	if ins.Op == machine.OpFloatReload {
		dst, src = src, dst
	}
	e.line(FloatMemorySize(ins, e.stack, e.goHomes), "movsd %s, %s", dst, src)
	return nil
}
