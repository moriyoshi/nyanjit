package amd64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/machine"
)

func LiteralSize(ins machine.Instruction) int {
	if machine.VectorNumber(ins.A) >= 8 {
		return 9
	}
	return 8
}
func (e *ByteAssembler) Literal(ins machine.Instruction) error {
	if ins.Op == machine.OpLiteralData {
		e.code = append(e.code, ins.Data...)
		return nil
	}
	n := machine.VectorNumber(ins.A)
	prefix := byte(0xf2)
	op := byte(0x10)
	if ins.Bits == 32 {
		prefix = 0xf3
	}
	if ins.Bits == 128 {
		prefix, op = 0xf3, 0x6f
	}
	e.code = append(e.code, prefix)
	if n >= 8 {
		e.code = append(e.code, 0x44)
	}
	e.code = append(e.code, 0x0f, op, 0x05|(n&7)<<3)
	d := int32(int64(ins.Disp) - int64(e.pc+LiteralSize(ins)))
	e.code = append(e.code, byte(d), byte(d>>8), byte(d>>16), byte(d>>24))
	return nil
}
func (e *TextAssembler) Literal(ins machine.Instruction) error {
	if ins.Op == machine.OpLiteralData {
		e.line(len(ins.Data), ".byte %s", fmt.Sprintf("% x", ins.Data))
		return nil
	}
	op := "movsd"
	if ins.Bits == 32 {
		op = "movss"
	}
	if ins.Bits == 128 {
		op = "movdqu"
	}
	e.line(LiteralSize(ins), "%s xmm%d, [rip + %d] # literal %#x", op, machine.VectorNumber(ins.A), int64(ins.Disp)-int64(e.pc+LiteralSize(ins)), ins.Disp)
	return nil
}
