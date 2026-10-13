package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// validCompareSet reports whether the operands fit: GP registers, a defined
// integer condition and Bits 8, 16, 32 or 64. A shifted comparison writes r19
// before it reads b, so b must not be r19.
func validCompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) bool {
	shifted := w < 64 && machine.ComparisonFor(c) == machine.CompareSigned
	return c <= op.BranchAbove && validWidth(w) && validGeneralRegisters(d, a, b) && (b != 19 || !shifted)
}

// CompareSet writes to d 1 when the w-bit integers in a and b satisfy c,
// else 0. Zero-extended operands compare directly for equality and unsigned
// conditions; signed narrow ones compare shifted to the top of r19 and r20,
// which orders them as their w-bit values. slt and sltu decide the strict
// orders, swapped for > and ≤; xori 1 negates ≥ and ≤; xor then sltui 1
// (seqz) or sltu from r0 (snez) decides equality. Both inputs are read before
// d is written, so d may alias either.
func (e *ByteAssembler) CompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validCompareSet(c, w, d, a, b)) {
		return
	}
	if w < 64 && machine.ComparisonFor(c) == machine.CompareSigned {
		e.shift(op.OpShiftLeft, 19, a, 64-w)
		e.shift(op.OpShiftLeft, 20, b, 64-w)
		a, b = 19, 20
	}
	switch c {
	case op.BranchEqual:
		e.rrr(0x00158000, "xor", d, a, b)
		e.ri(0x02400000, "sltui", d, d, 1)
		return
	case op.BranchNotEqual:
		e.rrr(0x00158000, "xor", d, a, b)
		e.rrr(0x00128000, "sltu", d, 0, d)
		return
	}
	code, name := uint32(0x00128000), "sltu"
	if machine.ComparisonFor(c) == machine.CompareSigned {
		code, name = 0x00120000, "slt"
	}
	switch c {
	case op.BranchGreater, op.BranchLessEqual, op.BranchAbove, op.BranchBelowEqual:
		a, b = b, a
	}
	e.rrr(code, name, d, a, b)
	switch c {
	case op.BranchGreaterEqual, op.BranchLessEqual, op.BranchAboveEqual, op.BranchBelowEqual:
		e.ri(0x03c00000, "xori", d, d, 1)
	}
}
