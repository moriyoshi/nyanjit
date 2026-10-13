package arm64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// integerConditionCodes maps the integer branch conditions and the sign
// flags, indexed by op.BranchCondition, to AArch64 condition codes.
var integerConditionCodes = [...]uint32{condEQ, condNE, condLT, condGE, condLE, condGT, 3, 2, condLS, condHI, condMI, condPL}

// swappedCondition is the condition of b c a for a c b.
var swappedCondition = [...]op.BranchCondition{
	op.BranchEqual: op.BranchEqual, op.BranchNotEqual: op.BranchNotEqual,
	op.BranchLess: op.BranchGreater, op.BranchGreaterEqual: op.BranchLessEqual,
	op.BranchLessEqual: op.BranchGreaterEqual, op.BranchGreater: op.BranchLess,
	op.BranchBelow: op.BranchAbove, op.BranchAboveEqual: op.BranchBelowEqual,
	op.BranchBelowEqual: op.BranchAboveEqual, op.BranchAbove: op.BranchBelow,
}

// compareSetSignedNarrow reports a signed 8- or 16-bit OpCompareSet, which
// sign-extends its operands first.
func compareSetSignedNarrow(c op.BranchCondition, w uint8) bool {
	return w < 32 && machine.ComparisonFor(c) == machine.CompareSigned
}

// validCompareSet reports whether the operands fit: GP registers, a defined
// integer condition and Bits 8, 16, 32 or 64. The signed narrow form uses an
// extended-register compare, where register 31 is SP, so it takes X0-X30.
func validCompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) bool {
	return c <= op.BranchAbove && validWidth(w) && validGeneralRegisters(d, a, b) &&
		(!compareSetSignedNarrow(c, w) || d < 31 && a < 31 && b < 31)
}

// compareSetPlan returns the words of an OpCompareSet and the condition its
// CSET tests: CMP Wa, Wb (Xa, Xb at 64 bits) then CSET Wd, which zero-extends.
// Zero-extended narrow operands compare alike at 32 bits for equality and
// unsigned conditions. A signed narrow compare sign-extends a into Wd and
// compares with b sign-extended by the operand (SXTB or SXTH); when d is b it
// extends b instead and swaps the condition. d is the only register written.
func compareSetPlan(c op.BranchCondition, w uint8, d, a, b backend.Register) ([]uint32, op.BranchCondition) {
	if !compareSetSignedNarrow(c, w) {
		cmp := uint32(0x6b00001f) | uint32(b)<<16 | uint32(a)<<5
		if w == 64 {
			cmp |= 1 << 31
		}
		return []uint32{cmp, cset(d, c)}, c
	}
	if d == b {
		a, b, c = b, a, swappedCondition[c]
	}
	extend, option := uint32(0x13001c00), uint32(4) // SXTB, extended operand SXTB
	if w == 16 {
		extend, option = 0x13003c00, 5
	}
	return []uint32{
		extend | uint32(a)<<5 | uint32(d),
		0x6b20001f | uint32(b)<<16 | option<<13 | uint32(d)<<5,
		cset(d, c),
	}, c
}

// cset encodes CSET Wd, c, which is CSINC Wd, WZR, WZR with the inverse code.
func cset(d backend.Register, c op.BranchCondition) uint32 {
	return 0x1a9f07e0 | (integerConditionCodes[c]^1)<<12 | uint32(d)
}

// CompareSet implements OpCompareSet: W d is 1 when the w-bit integers in a
// and b satisfy c, else 0. It writes NZCV.
func (e *ByteAssembler) CompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validCompareSet(c, w, d, a, b)) {
		return
	}
	words, _ := compareSetPlan(c, w, d, a, b)
	e.words = append(e.words, words...)
}

// CompareSet renders the sequence compareSetPlan encodes.
func (e *TextAssembler) CompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.CompareSet(c, w, d, a, b) }) {
		return
	}
	_, tested := compareSetPlan(c, w, d, a, b)
	switch {
	case compareSetSignedNarrow(c, w):
		left, right := a, b
		if d == b {
			left, right = b, a
		}
		extend := "sxtb"
		if w == 16 {
			extend = "sxth"
		}
		e.line("%s W%d, W%d", extend, d, left)
		e.line("cmp W%d, W%d, %s", d, right, extend)
	case w == 64:
		e.line("cmp X%d, X%d", a, b)
	default:
		e.line("cmp W%d, W%d", a, b)
	}
	e.line("cset W%d, %s", d, conditionMnemonics[integerConditionCodes[tested]])
}

// compareSetSize is the byte size of a valid OpCompareSet.
func compareSetSize(c op.BranchCondition, w uint8) int {
	if compareSetSignedNarrow(c, w) {
		return 12
	}
	return 8
}

// branchConditionCode returns the AArch64 condition code that tests c: an
// integer condition, a sign flag test or an overflow test (VS, VC).
func branchConditionCode(c op.BranchCondition) (uint32, bool) {
	switch {
	case int(c) < len(integerConditionCodes):
		return integerConditionCodes[c], true
	case c == machine.BranchOverflow:
		return condVS, true
	case c == machine.BranchNotOverflow:
		return condVC, true
	}
	return 0, false
}

// validBranchCondition reports whether branchConditionCode encodes c.
func validBranchCondition(c op.BranchCondition) bool {
	_, ok := branchConditionCode(c)
	return ok
}

// branchConditionName is the mnemonic suffix of c in branches and selects.
func branchConditionName(c op.BranchCondition) string {
	if c >= op.BranchBelow && c <= op.BranchAbove {
		return [...]string{"lo", "hs", "ls", "hi"}[c-op.BranchBelow]
	}
	code, _ := branchConditionCode(c)
	return conditionMnemonics[code]
}
