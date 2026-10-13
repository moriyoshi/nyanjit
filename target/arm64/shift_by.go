package arm64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// A single plan feeds both assemblers and PC accounting. X16/X17 are reserved
// expansion temporaries. Read the full count before writing an aliased output.
type shiftByInstruction struct {
	word uint32
	text string
}

func shiftByPlan(op ir.Opcode, width uint8, dst, src, count machine.Register) []shiftByInstruction {
	var out []shiftByInstruction
	emit := func(word uint32, format string, args ...any) {
		out = append(out, shiftByInstruction{word, fmt.Sprintf(format, args...)})
	}
	narrow := func(d, s machine.Register, w uint8) {
		emit(0xD3400000|uint32(w-1)<<10|uint32(s)<<5|uint32(d), "ubfm X%d, X%d, #0, #%d", d, s, w-1)
	}
	sign := func(d, s machine.Register, w uint8) {
		emit(0x93400000|uint32(w-1)<<10|uint32(s)<<5|uint32(d), "sbfm X%d, X%d, #0, #%d", d, s, w-1)
	}
	if op == ir.OpRotateLeft || op == ir.OpRotateRight {
		if width < 32 {
			// Replicate into a 32-bit word so hardware rotation has the requested period.
			emit(0x2A0003E0|uint32(src)<<16|16, "mov W16, W%d", src)
			for s := width; s < 32; s *= 2 {
				emit(0x2A000000|16<<16|uint32(s)<<10|16<<5|16, "orr W16, W16, W16, lsl #%d", s)
			}
			src = 16
		}
		if op == ir.OpRotateLeft {
			emit(0xCB0003E0|uint32(count)<<16|17, "neg X17, X%d", count)
			count = 17
		}
		base := uint32(0x9AC02C00)
		r := "X"
		if width <= 32 {
			base &^= 1 << 31
			r = "W"
		}
		emit(base|uint32(count)<<16|uint32(src)<<5|uint32(dst), "rorv %s%d, %s%d, %s%d", r, dst, r, src, r, count)
		if width < 32 {
			narrow(dst, dst, width)
		}
		return out
	}
	fill := machine.Register(31)
	if op == ir.OpShiftRightArithmetic {
		if width < 64 {
			sign(16, src, width)
			src = 16
		}
		emit(0x9340FC00|63<<16|uint32(src)<<5|17, "asr X17, X%d, #63", src)
		fill = 17
	}
	emit(0xF100001F|uint32(width)<<10|uint32(count)<<5, "cmp X%d, #%d", count, width)
	base := map[ir.Opcode]uint32{ir.OpShiftLeft: 0x9AC02000, ir.OpShiftRight: 0x9AC02400, ir.OpShiftRightArithmetic: 0x9AC02800}[op]
	mnemonic := map[ir.Opcode]string{ir.OpShiftLeft: "lslv", ir.OpShiftRight: "lsrv", ir.OpShiftRightArithmetic: "asrv"}[op]
	emit(base|uint32(count)<<16|uint32(src)<<5|uint32(dst), "%s X%d, X%d, X%d", mnemonic, dst, src, count)
	emit(0x9A800000|uint32(fill)<<16|3<<12|uint32(dst)<<5|uint32(dst), "csel X%d, X%d, X%d, lo", dst, dst, fill)
	if width < 64 {
		narrow(dst, dst, width)
	}
	return out
}

// ShiftBy implements saturating shifts and modulo-width rotations.
func (e *ByteAssembler) ShiftBy(op ir.Opcode, width uint8, dst, src, count machine.Register) {
	for _, i := range shiftByPlan(op, width, dst, src, count) {
		e.words = append(e.words, i.word)
	}
}

// ShiftBy renders exactly the plan used by native emission.
func (e *TextAssembler) ShiftBy(op ir.Opcode, width uint8, dst, src, count machine.Register) {
	for _, i := range shiftByPlan(op, width, dst, src, count) {
		e.line("%s", i.text)
	}
}
func (Policy) CanShiftBy(width uint8) bool {
	return width == 8 || width == 16 || width == 32 || width == 64
}
