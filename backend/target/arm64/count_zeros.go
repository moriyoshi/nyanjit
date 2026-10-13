package arm64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func (Policy) CanCountZeros(op op.Opcode, w uint8) bool {
	return w == 8 || w == 16 || w == 32 || w == 64
}
func countZerosPlan(operation op.Opcode, w uint8, d, s backend.Register) []shiftByInstruction {
	var out []shiftByInstruction
	emit := func(word uint32, format string, args ...any) {
		out = append(out, shiftByInstruction{word, fmt.Sprintf(format, args...)})
	}
	base := uint32(0xdac00000)
	r := "X"
	if w < 64 {
		base &^= 1 << 31
		r = "W"
	}
	if operation == op.OpTrailingZeros {
		emit(base|uint32(s)<<5|uint32(d), "rbit %s%d, %s%d", r, d, r, s)
		s = d
	}
	emit(base|0x1000|uint32(s)<<5|uint32(d), "clz %s%d, %s%d", r, d, r, s)
	if w < 32 {
		if operation == op.OpLeadingZeros {
			emit(0x51000000|uint32(32-w)<<10|uint32(d)<<5|uint32(d), "sub W%d, W%d, #%d", d, d, 32-w)
		} else {
			emit(0x7100001f|uint32(w)<<10|uint32(d)<<5, "cmp W%d, #%d", d, w)
			emit(0x52800010|uint32(w)<<5, "mov W16, #%d", w)
			emit(0x1a800000|16<<16|3<<12|uint32(d)<<5|uint32(d), "csel W%d, W%d, W16, lo", d, d)
		}
	}
	return out
}
func (e *ByteAssembler) CountZeros(operation op.Opcode, w uint8, d, s backend.Register) {
	if !e.require((validGeneralRegisters(d, s)) && (validWidth(w)) && (operation == op.OpLeadingZeros || operation == op.OpTrailingZeros)) {
		return
	}

	for _, i := range countZerosPlan(operation, w, d, s) {
		e.words = append(e.words, i.word)
	}
}
func (e *TextAssembler) CountZeros(op op.Opcode, w uint8, d, s backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.CountZeros(op, w, d, s) }) {
		return
	}
	for _, i := range countZerosPlan(op, w, d, s) {
		e.line("%s", i.text)
	}
}
