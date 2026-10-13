package i386

import "github.com/moriyoshi/nyanjit/backend"

// CanReverseBytes reports native byte reversal of one word: BSWAP for 32
// bits and ROL by 8 for 16. Selection reverses a 64-bit pair as two words.
func (Policy) CanReverseBytes(width uint8) bool { return width == 16 || width == 32 }

func validReverseBytes(w uint8, d, s backend.Register) bool {
	return (w == 16 || w == 32) && validGeneralRegisters(d, s)
}

// reverseBytesPlan copies s to d and reverses d: BSWAP, or ROL d16, 8 for a
// zero-extended 16-bit value, which writes flags.
func reverseBytesPlan(w uint8, d, s backend.Register, text bool) []expansionStep {
	x := &expansion{text: text}
	x.mov(d, s)
	if w == 16 {
		x.emit([]byte{0x66, 0xc1, modRM(3, 0, byte(d)), 8}, "rol %s, 8", name(d)[1:])
	} else {
		x.emit([]byte{0x0f, 0xc8 | byte(d)}, "bswap %s", name(d))
	}
	return x.resolve()
}

// ReverseBytes sets d to the w-bit integer in s with its bytes reversed.
func (e *ByteAssembler) ReverseBytes(w uint8, d, s backend.Register) {
	if !e.require(validReverseBytes(w, d, s)) {
		return
	}
	emitExpansion(e, reverseBytesPlan(w, d, s, false))
}
func (e *TextAssembler) ReverseBytes(w uint8, d, s backend.Register) {
	if !e.require(validReverseBytes(w, d, s)) {
		return
	}
	recordExpansion(e, reverseBytesPlan(w, d, s, true))
}
