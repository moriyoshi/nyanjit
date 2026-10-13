package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func (Policy) CanMulHigh(w uint8) bool   { return w == 8 || w == 16 || w == 32 || w == 64 }
func (Policy) CanWordCarry(w uint8) bool { return w == 64 }
func (e *ByteAssembler) MulHigh(w uint8, d, a, b backend.Register) {
	if !e.require((validGeneralRegisters(d, a, b)) && (validWidth(w))) {
		return
	}

	if w == 64 {
		e.words = append(e.words, rWord(1, uint32(b), uint32(a), 3, uint32(d), 0x33))
	} else {
		e.Binary(op.OpMul, 64, d, a, b)
		e.Shift(op.OpShiftRight, 64, d, d, w)
	}
}

// CanMulHighSigned reports native signed high products for every width.
func (Policy) CanMulHighSigned(w uint8) bool { return validWidth(w) }

// mulHighSignedWords returns the signed high product of B and C into A. A
// narrow width w shifts both operands to the top of x30 and x31, so MULH
// gives the full product shifted left by 64-2w and its top w bits are the
// high half, zero-extended by SRLI.
func mulHighSignedWords(w uint8, d, a, b backend.Register) []uint32 {
	if w == 64 {
		return []uint32{rWord(1, uint32(b), uint32(a), 1, uint32(d), 0x33)}
	}
	k := int64(64 - w)
	return []uint32{
		iWord(k, uint32(a), 1, 30, 0x13),
		iWord(k, uint32(b), 1, 31, 0x13),
		rWord(1, 31, 30, 1, uint32(d), 0x33),
		iWord(k, uint32(d), 5, uint32(d), 0x13),
	}
}

func (e *ByteAssembler) MulHighSigned(w uint8, d, a, b backend.Register) {
	if !e.require(validGeneralRegisters(d, a, b) && validWidth(w)) {
		return
	}
	e.words = append(e.words, mulHighSignedWords(w, d, a, b)...)
}

func wordCarryWords(ins machine.Instruction) []uint32 {
	d, a, b := uint32(ins.A), uint32(ins.B), uint32(ins.C)
	sub := ins.Arithmetic == op.OpSub
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
	if !e.require(ins.Op == machine.OpCarryBit || ins.Op == machine.OpWordCarry) {
		return
	}
	if !e.require((validInstructionRegisters(ins))) {
		return
	}

	e.words = append(e.words, wordCarryWords(ins)...)
}
