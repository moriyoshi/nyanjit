package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// compareSetScratch reports a signed comparison narrower than 64 bits, which
// shifts its operands to the top of x30 and x31 first.
func compareSetScratch(c op.BranchCondition, w uint8) bool {
	return w < 64 && machine.ComparisonFor(c) == machine.CompareSigned
}

// validCompareSet reports whether the operands fit: GP registers, a defined
// integer condition and Bits 8, 16, 32 or 64. A shifted comparison writes x30
// before it reads b, so b must not be x30.
func validCompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) bool {
	return c <= op.BranchAbove && validWidth(w) && validGeneralRegisters(d, a, b) && (b != 30 || !compareSetScratch(c, w))
}

// compareSetWords sets d to 1 when a c b holds. Zero-extended operands
// compare directly for equality and unsigned conditions; signed narrow ones
// compare shifted to the top of x30 and x31, which orders them as their
// w-bit values. SLT and SLTU decide the strict orders, swapped for > and
// ≤; XORI 1 negates ≥ and ≤; XOR then SEQZ or SNEZ decides equality.
func compareSetWords(c op.BranchCondition, w uint8, d, a, b backend.Register) []uint32 {
	rd, x, y := uint32(d), uint32(a), uint32(b)
	var out []uint32
	if compareSetScratch(c, w) {
		k := int64(64 - w)
		out = append(out, iWord(k, x, 1, 30, 0x13), iWord(k, y, 1, 31, 0x13))
		x, y = 30, 31
	}
	switch c {
	case op.BranchEqual:
		return append(out, rWord(0, y, x, 4, rd, 0x33), iWord(1, rd, 3, rd, 0x13))
	case op.BranchNotEqual:
		return append(out, rWord(0, y, x, 4, rd, 0x33), rWord(0, rd, 0, 3, rd, 0x33))
	}
	funct3 := uint32(3) // SLTU
	if machine.ComparisonFor(c) == machine.CompareSigned {
		funct3 = 2 // SLT
	}
	switch c {
	case op.BranchGreater, op.BranchLessEqual, op.BranchAbove, op.BranchBelowEqual:
		x, y = y, x
	}
	out = append(out, rWord(0, y, x, funct3, rd, 0x33))
	switch c {
	case op.BranchGreaterEqual, op.BranchLessEqual, op.BranchAboveEqual, op.BranchBelowEqual:
		out = append(out, iWord(1, rd, 4, rd, 0x13))
	}
	return out
}

// CompareSet writes 1 to d when the w-bit integers in a and b satisfy c,
// else 0.
func (e *ByteAssembler) CompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validCompareSet(c, w, d, a, b)) {
		return
	}
	e.words = append(e.words, compareSetWords(c, w, d, a, b)...)
}
