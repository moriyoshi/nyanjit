package arm64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func (Policy) CanMulHigh(w uint8) bool   { return w == 8 || w == 16 || w == 32 || w == 64 }
func (Policy) CanWordCarry(w uint8) bool { return w == 32 || w == 64 }
func (e *ByteAssembler) MulHigh(w uint8, d, a, b backend.Register) {
	if !e.require((validGeneralRegisters(d, a, b)) && (validWidth(w))) {
		return
	}

	if w == 64 {
		e.words = append(e.words, 0x9bc07c00|uint32(b)<<16|uint32(a)<<5|uint32(d))
	} else {
		e.Binary(op.OpMul, 64, d, a, b)
		e.Shift(op.OpShiftRight, 64, d, d, w)
	}
}
func (e *TextAssembler) MulHigh(w uint8, d, a, b backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.MulHigh(w, d, a, b) }) {
		return
	}
	if w == 64 {
		e.line("umulh X%d, X%d, X%d", d, a, b)
	} else {
		e.Binary(op.OpMul, 64, d, a, b)
		e.Shift(op.OpShiftRight, 64, d, d, w)
	}
}

// CanMulHighSigned reports native signed high products: SMULH for 64 bits,
// SMULL for 32, and a sign-extended 32-bit MUL for 8 and 16.
func (Policy) CanMulHighSigned(w uint8) bool { return validWidth(w) }

// mulHighSignedWords returns the encoding of the signed high product of
// B and C into A. Narrow operands are sign-extended into X16 and X17.
func mulHighSignedWords(w uint8, d, a, b backend.Register) []uint32 {
	switch w {
	case 64:
		return []uint32{0x9b407c00 | uint32(b)<<16 | uint32(a)<<5 | uint32(d)}
	case 32:
		return []uint32{0x9b207c00 | uint32(b)<<16 | uint32(a)<<5 | uint32(d), 0xd3400000 | 32<<16 | 63<<10 | uint32(d)<<5 | uint32(d)}
	}
	return []uint32{
		0x13000000 | uint32(w-1)<<10 | uint32(a)<<5 | 16,
		0x13000000 | uint32(w-1)<<10 | uint32(b)<<5 | 17,
		0x1b007c00 | 17<<16 | 16<<5 | uint32(d),
		0x53000000 | uint32(w)<<16 | uint32(2*w-1)<<10 | uint32(d)<<5 | uint32(d),
	}
}

func (e *ByteAssembler) MulHighSigned(w uint8, d, a, b backend.Register) {
	if !e.require(validGeneralRegisters(d, a, b) && validWidth(w)) {
		return
	}
	e.words = append(e.words, mulHighSignedWords(w, d, a, b)...)
}

func (e *TextAssembler) MulHighSigned(w uint8, d, a, b backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.MulHighSigned(w, d, a, b) }) {
		return
	}
	switch w {
	case 64:
		e.line("smulh X%d, X%d, X%d", d, a, b)
	case 32:
		e.line("smull X%d, W%d, W%d", d, a, b)
		e.line("lsr X%d, X%d, #32", d, d)
	default:
		e.line("sxt%s W16, W%d", narrowSuffix(w), a)
		e.line("sxt%s W17, W%d", narrowSuffix(w), b)
		e.line("mul W%d, W16, W17", d)
		e.line("ubfx W%d, W%d, #%d, #%d", d, d, w, w)
	}
}

func (e *ByteAssembler) WordCarry(ins machine.Instruction) {
	if !e.require(ins.Op == machine.OpCarryBit || ins.Op == machine.OpWordCarry) {
		return
	}
	if !e.require((validInstructionRegisters(&ins))) {
		return
	}

	width := ins.Bits
	sub := ins.Arithmetic == op.OpSub
	flags := func(a, b backend.Register) {
		base := uint32(0xab000000)
		if sub {
			base = 0xeb000000
		}
		if width == 32 {
			base &^= 1 << 31
		}
		e.words = append(e.words, base|uint32(b)<<16|uint32(a)<<5|16)
	}
	if ins.Op == machine.OpCarryBit {
		if ins.CarryMode != machine.CarryFlags {
			flags(ins.B, ins.C)
		}
		base := uint32(0x9a9f37e0)
		if sub {
			base = 0x9a9f27e0
		}
		e.words = append(e.words, base|uint32(ins.A))
		return
	}
	switch ins.CarryMode {
	case machine.CarryOperands:
		flags(ins.D, ins.E)
	case machine.CarryValue, machine.CarryBitValue:
		e.BinaryImmediate(op.OpAnd, 64, 16, ins.D, 1)
		if sub {
			e.words = append(e.words, 0xeb0003ff|16<<16)
		} else {
			e.words = append(e.words, 0xf100001f|1<<10|16<<5)
		}
	}
	base := uint32(0xba000000)
	if sub {
		base = 0xfa000000
	}
	if width == 32 {
		base &^= 1 << 31
	}
	e.words = append(e.words, base|uint32(ins.C)<<16|uint32(ins.B)<<5|uint32(ins.A))
}
func (e *TextAssembler) WordCarry(ins machine.Instruction) {
	if !e.valid(func(enc *ByteAssembler) { enc.WordCarry(ins) }) {
		return
	}
	r := "X"
	if ins.Bits == 32 {
		r = "W"
	}
	sub := ins.Arithmetic == op.OpSub
	flags := func(a, b backend.Register) {
		op := "adds"
		if sub {
			op = "subs"
		}
		e.line("%s %s16, %s%d, %s%d", op, r, r, a, r, b)
	}
	if ins.Op == machine.OpCarryBit {
		if ins.CarryMode != machine.CarryFlags {
			flags(ins.B, ins.C)
		}
		cond := "hs"
		if sub {
			cond = "lo"
		}
		e.line("cset X%d, %s", ins.A, cond)
		return
	}
	switch ins.CarryMode {
	case machine.CarryOperands:
		flags(ins.D, ins.E)
	case machine.CarryValue, machine.CarryBitValue:
		e.BinaryImmediate(op.OpAnd, 64, 16, ins.D, 1)
		if sub {
			e.line("cmp XZR, X16")
		} else {
			e.line("cmp X16, #1")
		}
	}
	op := "adcs"
	if sub {
		op = "sbcs"
	}
	e.line("%s %s%d, %s%d, %s%d", op, r, ins.A, r, ins.B, r, ins.C)
}
func wordCarrySize(ins machine.Instruction) int {
	n := 1
	if ins.Op == machine.OpCarryBit {
		if ins.CarryMode != machine.CarryFlags {
			n++
		}
	} else {
		switch ins.CarryMode {
		case machine.CarryOperands:
			n++
		case machine.CarryValue, machine.CarryBitValue:
			n += 2
		}
	}
	return n * 4
}
