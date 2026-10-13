package amd64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func LiteralSize(ins machine.Instruction) int {
	if ins.Op == machine.OpLiteralData {
		return len(ins.Data)
	}
	if ins.Op != machine.OpLiteralLoad || !validInstructionRegisters(ins) {
		return 0
	}
	if backend.VectorNumber(ins.A) >= 8 {
		return 9
	}
	return 8
}
func (e *ByteAssembler) Literal(ins machine.Instruction) error {
	if !e.require(ins.Op == machine.OpLiteralLoad || ins.Op == machine.OpLiteralData) {
		return e.err
	}
	if !e.require((validInstructionRegisters(ins))) {
		return e.err
	}

	if ins.Op == machine.OpLiteralData {
		e.code = append(e.code, ins.Data...)
		return nil
	}
	if !e.displacement(e.pc+LiteralSize(ins), int(ins.Disp), 32, 1) {
		return e.err
	}
	n := backend.VectorNumber(ins.A)
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
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.Literal(ins) }); err != nil {
		return err
	}
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
	e.line(LiteralSize(ins), "%s xmm%d, [rip + %d] # literal %#x", op, backend.VectorNumber(ins.A), int64(ins.Disp)-int64(e.pc+LiteralSize(ins)), ins.Disp)
	return nil
}
