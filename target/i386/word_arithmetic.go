package i386

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func (Policy) CanMulHigh(w uint8) bool   { return w == 32 }
func (Policy) CanWordCarry(w uint8) bool { return w == 32 }
func (e *ByteAssembler) MulHigh(_ uint8, d, a, b machine.Register) {
	e.Move(0, a)
	e.MulWide(b)
	e.Move(d, 2)
}
func (e *TextAssembler) MulHigh(_ uint8, d, a, b machine.Register) {
	e.Move(0, a)
	e.MulWide(b)
	e.Move(d, 2)
}
func wordCarryPlan(ins machine.Instruction) []shiftByInstruction {
	var out []shiftByInstruction
	emit := func(code []byte, format string, args ...any) {
		out = append(out, shiftByInstruction{code: code, text: fmt.Sprintf(format, args...)})
	}
	move := func(d, s machine.Register) {
		if d != s {
			emit([]byte{0x89, modRM(3, byte(s), byte(d))}, "mov %s, %s", name(d), name(s))
		}
	}
	flags := func(a, b machine.Register) {
		move(3, a)
		opcode := byte(0x01)
		op := "add"
		if ins.Arithmetic == ir.OpSub {
			opcode = 0x29
			op = "sub"
		}
		emit([]byte{opcode, modRM(3, byte(b), 3)}, "%s EBX, %s", op, name(b))
	}
	if ins.Op == machine.OpCarryBit {
		if ins.CarryMode != machine.CarryFlags {
			flags(ins.B, ins.C)
		}
		emit([]byte{0x0f, 0x92, 0xc3}, "setb BL")
		emit([]byte{0x0f, 0xb6, modRM(3, byte(ins.A), 3)}, "movzx %s, BL", name(ins.A))
		return out
	}
	switch ins.CarryMode {
	case machine.CarryOperands:
		flags(ins.D, ins.E)
	case machine.CarryValue, machine.CarryBitValue:
		emit([]byte{0x0f, 0xba, modRM(3, 4, byte(ins.D)), 0}, "bt %s, 0", name(ins.D))
	}
	move(ins.A, ins.B)
	opcode := byte(0x11)
	op := "adc"
	if ins.Arithmetic == ir.OpSub {
		opcode = 0x19
		op = "sbb"
	}
	emit([]byte{opcode, modRM(3, byte(ins.C), byte(ins.A))}, "%s %s, %s", op, name(ins.A), name(ins.C))
	return out
}
func (e *ByteAssembler) WordCarry(ins machine.Instruction) {
	for _, i := range wordCarryPlan(ins) {
		e.code = append(e.code, i.code...)
	}
}
func (e *TextAssembler) WordCarry(ins machine.Instruction) {
	for _, i := range wordCarryPlan(ins) {
		e.record("%s", i.text)
		e.code = append(e.code, i.code...)
	}
}
