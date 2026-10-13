package arm64

import "github.com/moriyoshi/nyanjit/backend"

// CanReverseBytes reports native byte reversal at every width: REV for 64
// and 32 bits and REV16 for 16.
func (Policy) CanReverseBytes(width uint8) bool { return width == 16 || width == 32 || width == 64 }

func validReverseBytes(w uint8, d, s backend.Register) bool {
	return (w == 16 || w == 32 || w == 64) && validGeneralRegisters(d, s)
}

// reverseBytesWord encodes REV Xd, Xn (64), REV Wd, Wn (32) or REV16 Wd, Wn
// (16). REV16 swaps the bytes of each halfword, so a zero-extended 16-bit
// value stays zero-extended.
func reverseBytesWord(w uint8, d, s backend.Register) uint32 {
	code := uint32(0xdac00c00)
	switch w {
	case 32:
		code = 0x5ac00800
	case 16:
		code = 0x5ac00400
	}
	return code | uint32(s)<<5 | uint32(d)
}

// ReverseBytes sets d to the w-bit integer in s with its bytes reversed.
func (e *ByteAssembler) ReverseBytes(w uint8, d, s backend.Register) {
	if !e.require(validReverseBytes(w, d, s)) {
		return
	}
	e.words = append(e.words, reverseBytesWord(w, d, s))
}

// ReverseBytes renders reverseBytesWord.
func (e *TextAssembler) ReverseBytes(w uint8, d, s backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.ReverseBytes(w, d, s) }) {
		return
	}
	switch w {
	case 64:
		e.line("rev X%d, X%d", d, s)
	case 32:
		e.line("rev W%d, W%d", d, s)
	default:
		e.line("rev16 W%d, W%d", d, s)
	}
}
