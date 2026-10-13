package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// CanReverseBytes reports native byte reversal. The baseline rv64gc has
// none, so selection expands it to shifts and masks; with Zbb the compile
// target uses REV8.
func (Policy) CanReverseBytes(width uint8) bool { return false }

// validReverseBytes checks the operands. Without Zbb the expansion collects
// bytes in x31 through x30 and rereads s, so s must be neither.
func validReverseBytes(w uint8, d, s backend.Register, profile abi.ResolvedCPU) bool {
	return (w == 16 || w == 32 || w == 64) && validGeneralRegisters(d, s) && (profile.Has("zbb") || s != 30 && s != 31)
}

// reverseBytesWords reverses the w-bit integer in s into d: Zbb REV8 and a
// logical right shift by 64-w, or else each byte shifted out of s through x30
// and placed into x31, then moved to d.
func reverseBytesWords(w uint8, d, s backend.Register, profile abi.ResolvedCPU) []uint32 {
	rd, rs := uint32(d), uint32(s)
	if profile.Has("zbb") {
		out := []uint32{iWord(0x6b8, rs, 5, rd, 0x13)}
		if w < 64 {
			out = append(out, iWord(int64(64-w), rd, 5, rd, 0x13))
		}
		return out
	}
	var out []uint32
	n := int64(w / 8)
	for i := int64(0); i < n; i++ {
		part := rs
		if i != 0 {
			out = append(out, iWord(8*i, rs, 5, 30, 0x13)) // srli x30, s, 8i
			part = 30
		}
		if i != n-1 {
			out = append(out, iWord(0xff, part, 7, 30, 0x13)) // andi x30, part, 255
			part = 30
		}
		if shift := 8 * (n - 1 - i); shift != 0 {
			out = append(out, iWord(shift, part, 1, 30, 0x13)) // slli x30, part, shift
			part = 30
		}
		if i == 0 {
			out = append(out, iWord(0, part, 0, 31, 0x13)) // mv x31, part
		} else {
			out = append(out, rWord(0, part, 31, 6, 31, 0x33)) // or x31, x31, part
		}
	}
	if rd != 31 {
		out = append(out, iWord(0, 31, 0, rd, 0x13))
	}
	return out
}

// ReverseBytes sets d to the w-bit integer in s with its bytes reversed.
func (e *ByteAssembler) ReverseBytes(w uint8, d, s backend.Register) {
	if !e.require(validReverseBytes(w, d, s, e.profile)) {
		return
	}
	e.words = append(e.words, reverseBytesWords(w, d, s, e.profile)...)
}
