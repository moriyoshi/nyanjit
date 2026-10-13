package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// shiftBy consumes the count before writing dst, which may alias either input.
// R11/R12 are reserved expansion temporaries, never allocator candidates.
func (p *expansion) shiftBy(operation op.Opcode, width uint8, dst, source, count backend.Register) error {
	if width == 0 {
		width = 64
	}
	if width != 8 && width != 16 && width != 32 && width != 64 {
		return machine.ErrUnsupportedOperation
	}
	if operation == op.OpRotateLeft || operation == op.OpRotateRight {
		if operation == op.OpRotateRight {
			p.emit(Instruction{Op: Neg, A: 11, B: count})
			count = 11
		}
		if width < 32 {
			// Repeat the narrow pattern into a word before rotating. The low
			// result bits then have the same period as the semantic width.
			p.move(12, source)
			if width == 8 {
				p.emit(Instruction{Op: Rlwimi, A: 12, B: 12, Shift: 8, Mask: 16, MaskEnd: 23})
			}
			p.emit(Instruction{Op: Rlwimi, A: 12, B: 12, Shift: 16, Mask: 0, MaskEnd: 15})
			source = 12
		}
		if width == 64 {
			p.emit(Instruction{Op: Rldcl, A: dst, B: source, C: count})
		} else {
			p.emit(Instruction{Op: Rlwnm, A: dst, B: source, C: count, MaskEnd: 31})
		}
		return p.narrow(dst, dst, width)
	}
	var native Opcode
	switch operation {
	case op.OpShiftLeft:
		native = Sld
		if width == 32 {
			native = Slw
		}
	case op.OpShiftRight:
		native = Srd
		if width == 32 {
			native = Srw
		}
	case op.OpShiftRightArithmetic:
		native = Srad
		if width == 32 {
			native = Sraw
		} else if width < 32 {
			if err := p.signExtend(12, source, width); err != nil {
				return err
			}
			source = 12
		}
	default:
		return machine.ErrUnsupportedOperation
	}
	// POWER ignores count bits above bit 6 (word shifts: bit 5). Clamp the
	// entire unsigned count so 128, 256, and high-bit counts cannot wrap.
	p.emit(Instruction{Op: Addi, A: 11, Imm: int64(width)})
	p.emit(Instruction{Op: Cmpli, Bits: 64, B: count, Imm: int64(width)})
	p.emit(Instruction{Op: Isel, A: 11, B: count, C: 11, BI: 0})
	p.emit(Instruction{Op: native, A: dst, B: source, C: 11})
	return p.narrow(dst, dst, width)
}
