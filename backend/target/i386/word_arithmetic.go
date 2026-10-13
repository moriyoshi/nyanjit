package i386

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func (Policy) CanMulHigh(w uint8) bool   { return w == 32 }
func (Policy) CanWordCarry(w uint8) bool { return w == 32 }
func (e *ByteAssembler) MulHigh(_ uint8, d, a, b backend.Register) {
	if !e.require(validGeneralRegisters(d) && validGeneralRegisters(a) && validGeneralRegisters(b)) {
		return
	}

	e.Move(0, a)
	e.MulWide(b)
	e.Move(d, 2)
}
func (e *TextAssembler) MulHigh(_ uint8, d, a, b backend.Register) {
	if !e.require(validGeneralRegisters(d) && validGeneralRegisters(a) && validGeneralRegisters(b)) {
		return
	}

	e.Move(0, a)
	e.MulWide(b)
	e.Move(d, 2)
}

// CanMulHighSigned reports the native 32-bit signed high product; other
// widths use the selection fallback.
func (Policy) CanMulHighSigned(w uint8) bool { return w == 32 }

// validMulHighSigned reports whether IMUL C can follow MOV EAX, B.
func validMulHighSigned(w uint8, d, a, b backend.Register) bool {
	return w == 32 && validGeneralRegisters(d) && validGeneralRegisters(a) && validGeneralRegisters(b) && (b != 0 || a == 0)
}

func (e *ByteAssembler) MulHighSigned(w uint8, d, a, b backend.Register) {
	if !e.require(validMulHighSigned(w, d, a, b)) {
		return
	}
	e.Move(0, a)
	e.code = append(e.code, 0xf7, modRM(3, 5, byte(b)))
	e.Move(d, 2)
}

func (e *TextAssembler) MulHighSigned(w uint8, d, a, b backend.Register) {
	if !e.require(validMulHighSigned(w, d, a, b)) {
		return
	}
	e.Move(0, a)
	e.record("imul %s", name(b))
	e.ByteAssembler.code = append(e.ByteAssembler.code, 0xf7, modRM(3, 5, byte(b)))
	e.Move(d, 2)
}

func wordCarryPlan(ins machine.Instruction) []shiftByInstruction {
	var out []shiftByInstruction
	emit := func(code []byte, format string, args ...any) {
		out = append(out, shiftByInstruction{code: code, text: fmt.Sprintf(format, args...)})
	}
	move := func(d, s backend.Register) {
		if d != s {
			emit([]byte{0x89, modRM(3, byte(s), byte(d))}, "mov %s, %s", name(d), name(s))
		}
	}
	flags := func(a, b backend.Register) {
		move(3, a)
		opcode := byte(0x01)
		operation := "add"
		if ins.Arithmetic == op.OpSub {
			opcode = 0x29
			operation = "sub"
		}
		emit([]byte{opcode, modRM(3, byte(b), 3)}, "%s EBX, %s", operation, name(b))
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
	operation := "adc"
	if ins.Arithmetic == op.OpSub {
		opcode = 0x19
		operation = "sbb"
	}
	emit([]byte{opcode, modRM(3, byte(ins.C), byte(ins.A))}, "%s %s, %s", operation, name(ins.A), name(ins.C))
	return out
}
func (e *ByteAssembler) WordCarry(ins machine.Instruction) {
	if !e.require(validInstructionRegisters(ins)) {
		return
	}

	for _, i := range wordCarryPlan(ins) {
		e.code = append(e.code, i.code...)
	}
}
func (e *TextAssembler) WordCarry(ins machine.Instruction) {
	if !e.require(validInstructionRegisters(ins)) {
		return
	}

	for _, i := range wordCarryPlan(ins) {
		e.record("%s", i.text)
		e.code = append(e.code, i.code...)
	}
}
