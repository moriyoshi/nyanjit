package amd64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// CanReverseBytes reports native byte reversal: BSWAP for 32 and 64 bits and
// ROL by 8 for 16.
func (Policy) CanReverseBytes(width uint8) bool { return width == 16 || width == 32 || width == 64 }

func validReverseBytes(w uint8, d, s backend.Register) bool {
	return (w == 16 || w == 32 || w == 64) && validGeneralRegisters(d, s)
}

// reverseBytesPlan copies s to d and reverses d: BSWAP at 32 or 64 bits, or
// ROL d16, 8 for a zero-extended 16-bit value, which leaves the upper bits
// zero and writes flags.
func reverseBytesPlan(w uint8, d, s backend.Register) []expansionStep {
	var x expansion
	r, src := uint8(d), uint8(s)
	if r != src {
		x.emit(gpRR(w == 64, src, r, false, 0x89), "mov %s, %s", gpNames(w == 64)(r), gpNames(w == 64)(src))
	}
	switch w {
	case 16:
		x.emit(append(append([]byte{0x66}, gpRR(false, 0, r, false, 0xc1)...), 8), "rol %s, 8", amd64RegisterNames16[r])
	default:
		code := gpRR(w == 64, 1, r, false, 0x0f) // REX of a register in rm
		code[len(code)-1] = 0xc8 | r&7
		x.emit(code, "bswap %s", gpNames(w == 64)(r))
	}
	return x.resolve()
}

// ReverseBytes sets d to the w-bit integer in s with its bytes reversed.
func (e *ByteAssembler) ReverseBytes(w uint8, d, s backend.Register) {
	if !e.require(validReverseBytes(w, d, s)) {
		return
	}
	e.expansion(reverseBytesPlan(w, d, s))
}
func (e *TextAssembler) ReverseBytes(w uint8, d, s backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.ReverseBytes(w, d, s) }) {
		return
	}
	e.expansion(reverseBytesPlan(w, d, s))
}

// reverseBytesWritesFlags reports the 16-bit form's ROL.
func reverseBytesWritesFlags(ins machine.Instruction) bool {
	return ins.Op == machine.OpReverseBytes && ins.Bits == 16
}
