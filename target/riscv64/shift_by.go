package riscv64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// X30/X31 are reserved; the plan remains valid when dst aliases either input.
func shiftByWords(op ir.Opcode, width uint8, dst, src, count machine.Register) []uint32 {
	return shiftByCPUWords(op, width, dst, src, count, abi.CPUProfile{})
}
func shiftByCPUWords(op ir.Opcode, width uint8, dst, src, count machine.Register, p abi.CPUProfile) []uint32 {
	var out []uint32
	emit := func(w uint32) { out = append(out, w) }
	narrow := func(d machine.Register, w uint8) {
		emit(shiftWord(d, d, 64-w, false, false))
		emit(shiftWord(d, d, 64-w, true, false))
	}
	if op == ir.OpRotateLeft || op == ir.OpRotateRight {
		opcode := uint32(0x33)
		if width <= 32 {
			opcode = 0x3b
		}
		if width < 32 {
			// Repeat the narrow source to the word size before the rotate.
			emit(iWord(0, uint32(src), 0, 31, 0x13))
			for s := width; s < 32; s *= 2 {
				emit(shiftWord(31, 30, s, false, false))
				emit(rWord(0, 30, 31, 6, 31, 0x33))
			}
			src = 31
		}
		if p.Has("zbb") {
			f3 := uint32(1)
			if op == ir.OpRotateRight {
				f3 = 5
			}
			emit(rWord(0x30, uint32(count), uint32(src), f3, uint32(dst), opcode))
			if width < 64 {
				narrow(dst, width)
			}
			return out
		}
		emit(rWord(0x20, uint32(count), 0, 0, 30, 0x33)) // -count
		first, second := uint32(1), uint32(5)
		if op == ir.OpRotateRight {
			first, second = second, first
		}
		// Narrow repetition occupies X31. Compute the first half in dst only
		// after copying the source to X31, so input aliases cannot destroy it.
		if src != 31 {
			emit(iWord(0, uint32(src), 0, 31, 0x13))
			src = 31
		}
		emit(rWord(0, uint32(count), uint32(src), first, uint32(dst), opcode))
		emit(rWord(0, 30, uint32(src), second, 31, opcode))
		emit(rWord(0, 31, uint32(dst), 6, uint32(dst), 0x33))
		if width < 64 {
			narrow(dst, width)
		}
		return out
	}
	emit(iWord(int64(width), uint32(count), 3, 30, 0x13)) // sltiu X30,count,width
	if op == ir.OpShiftRightArithmetic {
		// Invalid counts become all ones; SRA then produces the sign fill.
		emit(iWord(-1, 30, 0, 30, 0x13))
		emit(rWord(0, uint32(count), 30, 6, 30, 0x33))
		if width < 64 {
			emit(shiftWord(src, 31, 64-width, false, false))
			emit(shiftWord(31, 31, 64-width, true, true))
			src = 31
		}
		emit(rWord(0x20, 30, uint32(src), 5, uint32(dst), 0x33))
	} else {
		emit(rWord(0x20, 30, 0, 0, 30, 0x33)) // valid => all ones, invalid => zero
		f3 := uint32(1)
		if op == ir.OpShiftRight {
			f3 = 5
		}
		opcode := uint32(0x33)
		if width == 32 {
			opcode = 0x3b
		}
		emit(rWord(0, uint32(count), uint32(src), f3, 31, opcode))
		emit(rWord(0, 30, 31, 7, uint32(dst), 0x33))
	}
	if width < 64 {
		narrow(dst, width)
	}
	return out
}

// ShiftBy implements the portable full-count semantics with baseline RV64IM.
func (e *ByteAssembler) ShiftBy(op ir.Opcode, width uint8, dst, src, count machine.Register) {
	e.words = append(e.words, shiftByCPUWords(op, width, dst, src, count, e.profile)...)
}
func (Policy) CanShiftBy(width uint8) bool {
	return width == 8 || width == 16 || width == 32 || width == 64
}
