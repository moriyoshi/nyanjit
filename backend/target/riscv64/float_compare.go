package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// floatCompareScratch reports whether condition c needs x31: the ordered
// test and its ordered inequality combine two compare results.
func floatCompareScratch(c op.FloatCondition) bool {
	return c >= op.FloatOrdered && c <= op.FloatUnorderedEqual
}

func validFloatCompareOperands(c op.FloatCondition, w uint8, d, a, b backend.Register) bool {
	return validGeneralRegisters(d) && validVectorRegister(a) && validVectorRegister(b) &&
		(w == 32 || w == 64) && c <= op.FloatUnorderedEqual && (d != 31 || !floatCompareScratch(c))
}

func validFloatCompare(ins machine.Instruction) bool {
	return ins.Op == machine.OpFloatCompare && ins.Imm <= uint64(op.FloatUnorderedEqual) &&
		validFloatCompareOperands(op.FloatCondition(ins.Imm), ins.Bits, ins.A, ins.B, ins.C)
}

// floatCompareWords expands OpFloatCompare with feq, flt and fle, which
// write 0 or 1 to a GP register and return 0 for a NaN operand. The .s forms
// read the NaN-boxed binary32 values every FP write here produces. Even
// conditions are ordered relations; each odd condition is its even partner's
// negation and appends xori 1. Ordered is the AND of the two self-equality
// tests, and OrderedNotEqual the OR of both strict orders, combined through
// x31. A is a GP register, so writing it first never clobbers the FP inputs.
func floatCompareWords(c op.FloatCondition, w uint8, d, a, b backend.Register) []uint32 {
	format := uint32(0x50)
	if w == 64 {
		format = 0x51
	}
	rd, x, y := uint32(d), uint32(backend.VectorNumber(a)), uint32(backend.VectorNumber(b))
	compare := func(funct3, rd, x, y uint32) uint32 { return rWord(format, y, x, funct3, rd, 0x53) }
	const fle, flt, feq = 0, 1, 2
	var out []uint32
	switch c &^ 1 {
	case op.FloatEqual:
		out = []uint32{compare(feq, rd, x, y)}
	case op.FloatLess:
		out = []uint32{compare(flt, rd, x, y)}
	case op.FloatLessEqual:
		out = []uint32{compare(fle, rd, x, y)}
	case op.FloatGreater:
		out = []uint32{compare(flt, rd, y, x)}
	case op.FloatGreaterEqual:
		out = []uint32{compare(fle, rd, y, x)}
	case op.FloatOrdered:
		out = []uint32{compare(feq, rd, x, x), compare(feq, 31, y, y), rWord(0, 31, rd, 7, rd, 0x33)}
	case op.FloatOrderedNotEqual:
		out = []uint32{compare(flt, rd, x, y), compare(flt, 31, y, x), rWord(0, 31, rd, 6, rd, 0x33)}
	}
	if c&1 != 0 {
		out = append(out, iWord(1, rd, 4, rd, 0x13))
	}
	return out
}

// FloatCompare writes 1 to d when the w-bit floats in a and b satisfy c.
func (e *ByteAssembler) FloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validFloatCompareOperands(c, w, d, a, b)) {
		return
	}
	e.words = append(e.words, floatCompareWords(c, w, d, a, b)...)
}
