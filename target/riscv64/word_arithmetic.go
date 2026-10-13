package riscv64

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func (Policy) CanMulHigh(w uint8) bool   { return w == 8 || w == 16 || w == 32 || w == 64 }
func (Policy) CanWordCarry(w uint8) bool { return w == 64 }
func (e *ByteAssembler) MulHigh(w uint8, d, a, b machine.Register) {
	if w == 64 {
		e.words = append(e.words, rWord(1, uint32(b), uint32(a), 3, uint32(d), 0x33))
	} else {
		e.Binary(ir.OpMul, 64, d, a, b)
		e.Shift(ir.OpShiftRight, 64, d, d, w)
	}
}
func wordCarryWords(ins machine.Instruction) []uint32 {
	d, a, b := uint32(ins.A), uint32(ins.B), uint32(ins.C)
	sub := ins.Arithmetic == ir.OpSub
	carry := func(dst, a, b uint32) []uint32 {
		if sub {
			return []uint32{rWord(0, b, a, 3, dst, 0x33)}
		}
		return []uint32{rWord(0, b, a, 0, 31, 0x33), rWord(0, a, 31, 3, dst, 0x33)}
	}
	if ins.Op == machine.OpCarryBit {
		return carry(d, a, b)
	}
	var words []uint32
	if ins.CarryMode == machine.CarryOperands {
		words = carry(30, uint32(ins.D), uint32(ins.E))
	} else {
		words = append(words, iWord(1, uint32(ins.D), 7, 30, 0x13))
	}
	f7 := uint32(0)
	if sub {
		f7 = 0x20
	}
	// Preserve both high operands until after carry extraction, and consume
	// the bit in X30 only after the high-word sum has been computed.
	words = append(words, rWord(f7, b, a, 0, 31, 0x33), rWord(f7, 30, 31, 0, d, 0x33))
	return words
}
func (e *ByteAssembler) WordCarry(ins machine.Instruction) {
	e.words = append(e.words, wordCarryWords(ins)...)
}
