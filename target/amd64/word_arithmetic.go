package amd64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func (Policy) CanMulHigh(w uint8) bool   { return w == 8 || w == 16 || w == 32 || w == 64 }
func (Policy) CanWordCarry(w uint8) bool { return w == 64 }
func mulHighPlan(w uint8, d, a, b machine.Register) []shiftByInstruction {
	return mulHighCPUPlan(w, d, a, b, abi.CPUProfile{})
}
func mulHighCPUPlan(w uint8, d, a, b machine.Register, profile abi.CPUProfile) []shiftByInstruction {
	var out []shiftByInstruction
	move := func(d, s machine.Register) {
		if d != s {
			out = append(out, shiftByInstruction{appendMovReg(nil, uint8(d), uint8(s)), fmt.Sprintf("mov %s, %s", amd64Name(d), amd64Name(s))})
		}
	}
	if w == 64 {
		if profile.Has("bmi2") {
			move(2, a)
			out = append(out, shiftByInstruction{vexBMI(d, b, 11, 3, 0xf6), fmt.Sprintf("mulx %s, R11, %s", amd64Name(d), amd64Name(b))})
			return out
		}
		move(0, a)
		out = append(out, shiftByInstruction{[]byte{0x48 | byte(b>>3), 0xf7, 0xe0 | byte(b&7)}, "mul " + amd64Name(b)})
		move(d, 2)
	} else {
		e := NewByteAssembler(0, 0)
		e.Binary(ir.OpMul, 64, d, a, b)
		if d != a {
			out = append(out, shiftByInstruction{e.code[:3], fmt.Sprintf("mov %s, %s", amd64Name(d), amd64Name(a))})
			e.code = e.code[3:]
		}
		out = append(out, shiftByInstruction{e.code, fmt.Sprintf("imul %s, %s", amd64Name(d), amd64Name(b))})
		e = NewByteAssembler(0, 0)
		e.Shift(ir.OpShiftRight, 64, d, d, w)
		out = append(out, shiftByInstruction{e.code, fmt.Sprintf("shr %s, %d", amd64Name(d), w)})
	}
	return out
}
func (e *ByteAssembler) MulHigh(w uint8, d, a, b machine.Register) {
	for _, i := range mulHighCPUPlan(w, d, a, b, e.profile) {
		e.code = append(e.code, i.code...)
	}
}
func (e *TextAssembler) MulHigh(w uint8, d, a, b machine.Register) {
	for _, i := range mulHighCPUPlan(w, d, a, b, e.profile) {
		e.line(len(i.code), "%s", i.text)
	}
}
func wordCarryPlan(ins machine.Instruction) []shiftByInstruction {
	var out []shiftByInstruction
	emit := func(code []byte, format string, args ...any) {
		out = append(out, shiftByInstruction{code, fmt.Sprintf(format, args...)})
	}
	move := func(d, s machine.Register) {
		if d != s {
			emit(appendMovReg(nil, uint8(d), uint8(s)), "mov %s, %s", amd64Name(d), amd64Name(s))
		}
	}
	flags := func(a, b machine.Register) {
		move(11, a)
		e := NewByteAssembler(0, 0)
		e.Binary(ins.Arithmetic, 64, 11, 11, b)
		op := "add"
		if ins.Arithmetic == ir.OpSub {
			op = "sub"
		}
		emit(e.code, "%s R11, %s", op, amd64Name(b))
	}
	if ins.Op == machine.OpCarryBit {
		if ins.CarryMode != machine.CarryFlags {
			flags(ins.B, ins.C)
		}
		emit(appendMovImm64(nil, uint8(ins.A), 0), "mov %s, 0", amd64RegisterNames32[ins.A])
		emit([]byte{0x40 | byte(ins.A>>3), 0x0f, 0x92, 0xc0 | byte(ins.A&7)}, "setb %s", amd64RegisterNames8[ins.A])
		return out
	}
	switch ins.CarryMode {
	case machine.CarryOperands:
		flags(ins.D, ins.E)
	case machine.CarryValue, machine.CarryBitValue:
		emit([]byte{0x48 | byte(ins.D>>3), 0x0f, 0xba, 0xe0 | byte(ins.D&7), 0}, "bt %s, 0", amd64Name(ins.D))
	}
	move(ins.A, ins.B)
	opcode := byte(0x11)
	op := "adc"
	if ins.Arithmetic == ir.OpSub {
		opcode = 0x19
		op = "sbb"
	}
	emit([]byte{0x48 | byte(ins.C>>3)<<2 | byte(ins.A>>3), opcode, 0xc0 | byte(ins.C&7)<<3 | byte(ins.A&7)}, "%s %s, %s", op, amd64Name(ins.A), amd64Name(ins.C))
	return out
}
func (e *ByteAssembler) WordCarry(ins machine.Instruction) {
	for _, i := range wordCarryPlan(ins) {
		e.code = append(e.code, i.code...)
	}
}
func (e *TextAssembler) WordCarry(ins machine.Instruction) {
	for _, i := range wordCarryPlan(ins) {
		e.line(len(i.code), "%s", i.text)
	}
}
