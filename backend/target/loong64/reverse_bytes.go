package loong64

import "github.com/moriyoshi/nyanjit/backend"

// CanReverseBytes reports native byte reversal at every width.
func (Policy) CanReverseBytes(width uint8) bool { return width == 16 || width == 32 || width == 64 }

// ReverseBytes sets d to the w-bit integer in s with its bytes reversed:
// revb.d at 64 bits; revb.2w and revb.2h reverse within each word and
// halfword, so a zero-extended 32- or 16-bit value stays zero-extended.
func (e *ByteAssembler) ReverseBytes(w uint8, d, s backend.Register) {
	if !e.require((w == 16 || w == 32 || w == 64) && validGeneralRegisters(d, s)) {
		return
	}
	switch w {
	case 64:
		e.rr(0x3c00, "revb.d", d, s)
	case 32:
		e.rr(0x3800, "revb.2w", d, s)
	default:
		e.rr(0x3000, "revb.2h", d, s)
	}
}
