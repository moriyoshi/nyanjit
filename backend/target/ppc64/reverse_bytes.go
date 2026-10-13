package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// CanReverseBytes reports byte reversal at every width, built from rotate
// and insert instructions every POWER8 has.
func (Policy) CanReverseBytes(width uint8) bool { return width == 16 || width == 32 || width == 64 }

// reverseWord reverses the 16 or 32 bits of s into d, which must differ
// from s: a rotate into d, then each remaining byte inserted from s.
func (p *expansion) reverseWord(w uint8, d, s backend.Register) {
	if w == 16 {
		p.emit(Instruction{Op: Rlwinm, A: d, B: s, Shift: 24, Mask: 24, MaskEnd: 31})
		p.emit(Instruction{Op: Rlwimi, A: d, B: s, Shift: 8, Mask: 16, MaskEnd: 23})
		return
	}
	p.emit(Instruction{Op: Rlwinm, A: d, B: s, Shift: 8, Mask: 0, MaskEnd: 31})
	p.emit(Instruction{Op: Rlwimi, A: d, B: s, Shift: 24, Mask: 0, MaskEnd: 7})
	p.emit(Instruction{Op: Rlwimi, A: d, B: s, Shift: 24, Mask: 16, MaskEnd: 23})
}

// reverseBytes sets GP A to the Bits-wide integer in GP B with its bytes
// reversed. A 16- or 32-bit value is reversed through r11 when A is B. A
// 64-bit one reverses its low word into r11 and its high word, shifted into
// r12, into A, then merges them; A is written after B's last read.
func (p *expansion) reverseBytes(ins machine.Instruction) error {
	d, s, w := ins.A, ins.B, ins.Bits
	if w != 16 && w != 32 && w != 64 || d > 31 || s > 31 || d == 11 || d == 12 || s == 11 || s == 12 {
		return machine.ErrUnsupportedOperation
	}
	if w < 64 {
		if d != s {
			p.reverseWord(w, d, s)
			return nil
		}
		p.reverseWord(w, 11, s)
		p.move(d, 11)
		return nil
	}
	p.emit(Instruction{Op: Rldicl, A: 12, B: s, Shift: 32, Mask: 32}) // srdi r12, s, 32
	p.reverseWord(32, 11, s)
	p.reverseWord(32, d, 12)
	p.emit(Instruction{Op: Rldicr, A: 11, B: 11, Shift: 32, Mask: 31}) // sldi r11, r11, 32
	p.emit(Instruction{Op: Or, A: d, B: d, C: 11})
	return nil
}
