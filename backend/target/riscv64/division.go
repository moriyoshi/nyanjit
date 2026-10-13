package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func validDivide(ins machine.Instruction) bool {
	semantics := op.DivisionSemantics(ins.Imm)
	switch ins.Arithmetic {
	case op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned:
	default:
		return false
	}
	return ins.Op == machine.OpDivide && validInstructionRegisters(ins) && validWidth(ins.Bits) &&
		uint64(semantics) == ins.Imm && semantics.Valid()
}

// zeroExtendWords narrows src to its low w bits in dst; ANDI covers 8 bits
// in one word, wider masks do not fit its 12-bit immediate.
func zeroExtendWords(dst, src uint32, w uint8) []uint32 {
	if w == 8 {
		return []uint32{iWord(0xff, src, 7, dst, 0x13)}
	}
	return []uint32{iWord(int64(64-w), src, 1, dst, 0x13), iWord(int64(64-w), dst, 5, dst, 0x13)}
}

// divideWords expands OpDivide. RISC-V division never traps and already
// computes DivisionAllOnes: x/0 is all ones, x%0 is x, MIN/-1 is MIN and
// MIN%-1 is 0, so only the quotient's zero divisor differs between the
// semantics. DivisionZero adds the divisor's zero test (seqz, 0 or 1) to the
// all-ones quotient, which turns exactly x/0 into 0. The zero test lands in
// X31 before A is written, and every other word reads B and C before A is
// written, so A may alias either input.
func divideWords(ins machine.Instruction) []uint32 {
	d, x, y := uint32(ins.A), uint32(ins.B), uint32(ins.C)
	w := ins.Bits
	signed := ins.Arithmetic == op.OpDivSigned || ins.Arithmetic == op.OpRemSigned
	quotient := ins.Arithmetic == op.OpDiv || ins.Arithmetic == op.OpDivSigned
	semantics := op.DivisionSemantics(ins.Imm)
	zero := quotient && semantics == op.DivisionZero
	funct3 := uint32(4) // div, divu, rem, remu
	if !quotient {
		funct3 = 6
	}
	if !signed {
		funct3++
	}
	var out []uint32
	emit := func(words ...uint32) { out = append(out, words...) }
	seqz := func(dst, src uint32) uint32 { return iWord(1, src, 3, dst, 0x13) }
	add := func(dst, a, b uint32) uint32 { return rWord(0, b, a, 0, dst, 0x33) }
	switch {
	case !signed || w == 64:
		// Zero-extended operands divide as 64-bit unsigned values, so
		// narrow quotients and remainders already fit Bits except the
		// 64-bit all-ones x/0.
		if zero {
			emit(seqz(31, y))
		}
		emit(rWord(1, y, x, funct3, d, 0x33))
		if zero {
			emit(add(d, d, 31))
		} else if quotient && w < 64 && semantics == op.DivisionAllOnes {
			emit(zeroExtendWords(d, d, w)...)
		}
	case w == 32:
		// The *w forms divide the signed low words and sign-extend the
		// 32-bit result, which then needs zero-extension.
		if zero {
			emit(seqz(31, y))
		}
		emit(rWord(1, y, x, funct3, d, 0x3b))
		if zero {
			emit(add(d, d, 31))
		}
		emit(zeroExtendWords(d, d, 32)...)
	default:
		// Shifting both 8/16-bit operands to the top of the register
		// sign-extends them as one scale factor: the quotient is unchanged
		// (MIN/-1 cannot overflow 64 bits) and the remainder is scaled, so
		// a logical shift back yields it zero-extended.
		k := 64 - w
		emit(iWord(int64(k), x, 1, 30, 0x13), iWord(int64(k), y, 1, 31, 0x13))
		emit(rWord(1, 31, 30, funct3, 30, 0x33))
		if !quotient {
			emit(iWord(int64(k), 30, 5, d, 0x13))
			break
		}
		if zero {
			emit(seqz(31, 31), add(30, 30, 31))
		}
		emit(zeroExtendWords(d, 30, w)...)
	}
	return out
}

// divideScratch reports which reserved registers divideWords writes.
func divideScratch(ins machine.Instruction) (x30, x31 bool) {
	signed := ins.Arithmetic == op.OpDivSigned || ins.Arithmetic == op.OpRemSigned
	quotient := ins.Arithmetic == op.OpDiv || ins.Arithmetic == op.OpDivSigned
	if signed && ins.Bits < 32 {
		return true, true
	}
	return false, quotient && op.DivisionSemantics(ins.Imm) == op.DivisionZero
}

// Divide expands a quotient or remainder with the semantics in Imm.
func (e *ByteAssembler) Divide(ins machine.Instruction) {
	if !e.require(validDivide(ins)) {
		return
	}
	e.words = append(e.words, divideWords(ins)...)
}

func validFloatConvert(ins machine.Instruction) bool {
	if !validInstructionRegisters(ins) || ins.Bits != 32 && ins.Bits != 64 {
		return false
	}
	switch ins.Op {
	case machine.OpIntToFloat:
		return (ins.Arithmetic == op.OpFFromSigned || ins.Arithmetic == op.OpFFromUnsigned) && (ins.Imm == 32 || ins.Imm == 64)
	case machine.OpFloatToInt:
		semantics := op.ConversionSemantics(ins.Imm)
		return (ins.Arithmetic == op.OpFToSigned || ins.Arithmetic == op.OpFToUnsigned) && (ins.Disp == 32 || ins.Disp == 64) &&
			uint64(semantics) == ins.Imm && semantics.Valid()
	}
	return false
}

// floatConvertWords expands OpIntToFloat and OpFloatToInt with the F/D
// fcvt forms; rs2 selects the integer type (w, wu, l, lu). Int to float
// rounds in the dynamic mode and NaN-boxes a binary32 result itself.
// Float to int rounds toward zero (rtz). The hardware saturates out-of-range
// values but returns the maximum for NaN, so ConversionSaturate masks the
// result with the feq x, x ordered test. No FP register but A is written.
func floatConvertWords(ins machine.Instruction) []uint32 {
	if ins.Op == machine.OpIntToFloat {
		format, kind := uint32(0x68), uint32(0)
		if ins.Bits == 64 {
			format++
		}
		if ins.Imm == 64 {
			kind = 2
		}
		if ins.Arithmetic == op.OpFFromUnsigned {
			kind++
		}
		return []uint32{rWord(format, kind, uint32(ins.B), 7, uint32(backend.VectorNumber(ins.A)), 0x53)}
	}
	d, f := uint32(ins.A), uint32(backend.VectorNumber(ins.B))
	format, kind := uint32(0), uint32(0)
	if ins.Disp == 64 {
		format = 1
	}
	if ins.Bits == 64 {
		kind = 2
	}
	if ins.Arithmetic == op.OpFToUnsigned {
		kind++
	}
	out := []uint32{rWord(0x60|format, kind, f, 1, d, 0x53)}
	if op.ConversionSemantics(ins.Imm) == op.ConversionSaturate {
		// X31 = ordered ? all ones : 0, narrowed to 32 bits for a 32-bit
		// result so that the AND also zero-extends the sign-extended
		// fcvt.w(u) result.
		out = append(out, rWord(0x50|format, f, f, 2, 31, 0x53), rWord(0x20, 31, 0, 0, 31, 0x33))
		if ins.Bits == 32 {
			out = append(out, iWord(32, 31, 5, 31, 0x13))
		}
		return append(out, rWord(0, 31, d, 7, d, 0x33))
	}
	if ins.Bits == 32 {
		// RV64 sign-extends every 32-bit fcvt result, including fcvt.wu.
		out = append(out, zeroExtendWords(d, d, 32)...)
	}
	return out
}

// FloatConvert expands an integer/float conversion.
func (e *ByteAssembler) FloatConvert(ins machine.Instruction) {
	if !e.require(validFloatConvert(ins)) {
		return
	}
	e.words = append(e.words, floatConvertWords(ins)...)
}
