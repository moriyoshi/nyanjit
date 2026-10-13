package amd64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func (Policy) CanMulHigh(w uint8) bool   { return w == 8 || w == 16 || w == 32 || w == 64 }
func (Policy) CanWordCarry(w uint8) bool { return w == 64 }
func mulHighPlan(w uint8, d, a, b backend.Register) []shiftByInstruction {
	return mulHighCPUPlan(w, d, a, b, abi.ResolvedCPU{})
}
func mulHighCPUPlan(w uint8, d, a, b backend.Register, profile abi.ResolvedCPU) []shiftByInstruction {
	var out []shiftByInstruction
	move := func(d, s backend.Register) {
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
		e.Binary(op.OpMul, 64, d, a, b)
		if d != a {
			out = append(out, shiftByInstruction{e.code[:3], fmt.Sprintf("mov %s, %s", amd64Name(d), amd64Name(a))})
			e.code = e.code[3:]
		}
		out = append(out, shiftByInstruction{e.code, fmt.Sprintf("imul %s, %s", amd64Name(d), amd64Name(b))})
		e = NewByteAssembler(0, 0)
		e.Shift(op.OpShiftRight, 64, d, d, w)
		out = append(out, shiftByInstruction{e.code, fmt.Sprintf("shr %s, %d", amd64Name(d), w)})
	}
	return out
}
func (e *ByteAssembler) MulHigh(w uint8, d, a, b backend.Register) {
	if !e.require((validGeneralRegisters(d, a, b)) && (validWidth(w))) {
		return
	}

	for _, i := range mulHighCPUPlan(w, d, a, b, e.profile) {
		e.code = append(e.code, i.code...)
	}
}
func (e *TextAssembler) MulHigh(w uint8, d, a, b backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.MulHigh(w, d, a, b) }) {
		return
	}
	for _, i := range mulHighCPUPlan(w, d, a, b, e.profile) {
		e.line(len(i.code), "%s", i.text)
	}
}

// CanMulHighSigned reports native signed high products for every width.
func (Policy) CanMulHighSigned(w uint8) bool { return validWidth(w) }

// validMulHighSigned reports whether the operands fit the expansion: the
// 64-bit form loads B into RAX before IMUL reads C, and narrower forms
// sign-extend C into R11 before B.
func validMulHighSigned(w uint8, d, a, b backend.Register) bool {
	if !validGeneralRegisters(d, a, b) || !validWidth(w) {
		return false
	}
	if w == 64 {
		return b != 0 || a == 0
	}
	return a != 11
}

// mulHighSignedPlan forms the high Bits of signed B*C in A. 64 bits use the
// one-operand IMUL into RDX:RAX. Narrower widths multiply the sign-extended
// operands in 64 bits, where the product is exact, and shift its high half
// down; SHR zero-extends a 32-bit result and MOVZX a narrower one.
func mulHighSignedPlan(w uint8, d, a, b backend.Register) []shiftByInstruction {
	var out []shiftByInstruction
	piece := func(f func(*ByteAssembler), format string, args ...any) {
		e := NewByteAssembler(0, 0)
		f(e)
		if len(e.code) != 0 {
			out = append(out, shiftByInstruction{e.code, fmt.Sprintf(format, args...)})
		}
	}
	if w == 64 {
		piece(func(e *ByteAssembler) { e.Move(0, a) }, "mov RAX, %s", amd64Name(a))
		out = append(out, shiftByInstruction{[]byte{0x48 | byte(b>>3), 0xf7, 0xe8 | byte(b&7)}, "imul " + amd64Name(b)})
		piece(func(e *ByteAssembler) { e.Move(d, 2) }, "mov %s, RDX", amd64Name(d))
		return out
	}
	extend := func(dst, src backend.Register) {
		switch w {
		case 32:
			piece(func(e *ByteAssembler) { e.SignExtend(dst, src, w) }, "movsxd %s, %s", amd64Name(dst), amd64RegisterNames32[src])
		case 16:
			piece(func(e *ByteAssembler) { e.SignExtend(dst, src, w) }, "movsx %s, %s", amd64Name(dst), amd64RegisterNames16[src])
		default:
			piece(func(e *ByteAssembler) { e.SignExtend(dst, src, w) }, "movsx %s, %s", amd64Name(dst), amd64RegisterNames8[src])
		}
	}
	extend(11, b)
	extend(d, a)
	piece(func(e *ByteAssembler) { e.Binary(op.OpMul, 64, d, d, 11) }, "imul %s, R11", amd64Name(d))
	piece(func(e *ByteAssembler) { e.Shift(op.OpShiftRight, 64, d, d, w) }, "shr %s, %d", amd64Name(d), w)
	switch w {
	case 16:
		piece(func(e *ByteAssembler) { e.Narrow(d, d, w) }, "movzx %s, %s", amd64RegisterNames32[d], amd64RegisterNames16[d])
	case 8:
		piece(func(e *ByteAssembler) { e.Narrow(d, d, w) }, "movzx %s, %s", amd64RegisterNames32[d], amd64RegisterNames8[d])
	}
	return out
}

func (e *ByteAssembler) MulHighSigned(w uint8, d, a, b backend.Register) {
	if !e.require(validMulHighSigned(w, d, a, b)) {
		return
	}
	for _, i := range mulHighSignedPlan(w, d, a, b) {
		e.code = append(e.code, i.code...)
	}
}

func (e *TextAssembler) MulHighSigned(w uint8, d, a, b backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.MulHighSigned(w, d, a, b) }) {
		return
	}
	for _, i := range mulHighSignedPlan(w, d, a, b) {
		e.line(len(i.code), "%s", i.text)
	}
}

func wordCarryPlan(ins machine.Instruction) []shiftByInstruction {
	var out []shiftByInstruction
	emit := func(code []byte, format string, args ...any) {
		out = append(out, shiftByInstruction{code, fmt.Sprintf(format, args...)})
	}
	move := func(d, s backend.Register) {
		if d != s {
			emit(appendMovReg(nil, uint8(d), uint8(s)), "mov %s, %s", amd64Name(d), amd64Name(s))
		}
	}
	flags := func(a, b backend.Register) {
		move(11, a)
		e := NewByteAssembler(0, 0)
		e.Binary(ins.Arithmetic, 64, 11, 11, b)
		operation := "add"
		if ins.Arithmetic == op.OpSub {
			operation = "sub"
		}
		emit(e.code, "%s R11, %s", operation, amd64Name(b))
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
	operation := "adc"
	if ins.Arithmetic == op.OpSub {
		opcode = 0x19
		operation = "sbb"
	}
	emit([]byte{0x48 | byte(ins.C>>3)<<2 | byte(ins.A>>3), opcode, 0xc0 | byte(ins.C&7)<<3 | byte(ins.A&7)}, "%s %s, %s", operation, amd64Name(ins.A), amd64Name(ins.C))
	return out
}
func (e *ByteAssembler) WordCarry(ins machine.Instruction) {
	if !e.require(ins.Op == machine.OpCarryBit || ins.Op == machine.OpWordCarry) {
		return
	}
	if !e.require((validInstructionRegisters(ins))) {
		return
	}

	for _, i := range wordCarryPlan(ins) {
		e.code = append(e.code, i.code...)
	}
}
func (e *TextAssembler) WordCarry(ins machine.Instruction) {
	if !e.valid(func(enc *ByteAssembler) { enc.WordCarry(ins) }) {
		return
	}
	for _, i := range wordCarryPlan(ins) {
		e.line(len(i.code), "%s", i.text)
	}
}
