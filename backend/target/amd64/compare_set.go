package amd64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// integerConditionCodes are the x86 condition codes (the low nibble of Jcc,
// SETcc and CMOVcc) of the integer branch conditions and the sign flags,
// indexed by op.BranchCondition.
var integerConditionCodes = [...]byte{0x4, 0x5, 0xc, 0xd, 0xe, 0xf, 0x2, 0x3, 0x6, 0x7, 0x8, 0x9}

// conditionSuffixes name the condition codes in mnemonics.
var conditionSuffixes = [16]string{"o", "no", "b", "ae", "e", "ne", "be", "a", "s", "ns", "p", "np", "l", "ge", "le", "g"}

// conditionCode returns the condition code that tests c: an integer
// condition, a sign flag test or an overflow test.
func conditionCode(c op.BranchCondition) (byte, bool) {
	switch {
	case int(c) < len(integerConditionCodes):
		return integerConditionCodes[c], true
	case c == machine.BranchOverflow:
		return 0x0, true
	case c == machine.BranchNotOverflow:
		return 0x1, true
	}
	return 0, false
}

// validCompareSet reports whether the operands fit: three GP registers, a
// defined integer condition and Bits 8, 16, 32 or 64.
func validCompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) bool {
	return c <= op.BranchAbove && validWidth(w) && validGeneralRegisters(d, a, b)
}

// byteRR is gpRR for an 8-bit register-to-register form, where REX selects
// SPL..DIL in either field.
func byteRR(reg, rm uint8, opcode ...byte) []byte {
	code := gpRR(false, reg, rm, true, opcode...)
	if code[0]&0xf0 != 0x40 && reg >= 4 && reg < 8 {
		code = append([]byte{0x40}, code...)
	}
	return code
}

// compareSetPlan compares B with C at the operand width, then sets A's low
// byte from the condition and zero-extends it. Equality and unsigned
// conditions on zero-extended narrow operands compare at 32 bits. A may alias
// B or C, since both are read before A is written.
func compareSetPlan(c op.BranchCondition, w uint8, d, a, b backend.Register) []expansionStep {
	var x expansion
	r, left, right := uint8(d), uint8(a), uint8(b)
	if machine.ComparisonFor(c) != machine.CompareSigned && w < 32 {
		w = 32
	}
	switch w {
	case 8:
		x.emit(byteRR(right, left, 0x38), "cmp %s, %s", amd64RegisterNames8[left], amd64RegisterNames8[right])
	case 16:
		x.emit(append([]byte{0x66}, gpRR(false, right, left, false, 0x39)...), "cmp %s, %s", amd64RegisterNames16[left], amd64RegisterNames16[right])
	default:
		names := gpNames(w == 64)
		x.emit(gpRR(w == 64, right, left, false, 0x39), "cmp %s, %s", names(left), names(right))
	}
	cc := integerConditionCodes[c]
	x.emit(gpRR(false, 0, r, true, 0x0f, 0x90|cc), "set%s %s", conditionSuffixes[cc], amd64RegisterNames8[r])
	x.emit(gpRR(false, r, r, true, 0x0f, 0xb6), "movzx %s, %s", amd64RegisterNames32[r], amd64RegisterNames8[r])
	return x.resolve()
}

// CompareSet sets GP register d to 1 when the w-bit integers in a and b
// satisfy c, else to 0. It writes flags.
func (e *ByteAssembler) CompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validCompareSet(c, w, d, a, b)) {
		return
	}
	e.expansion(compareSetPlan(c, w, d, a, b))
}
func (e *TextAssembler) CompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.CompareSet(c, w, d, a, b) }) {
		return
	}
	e.expansion(compareSetPlan(c, w, d, a, b))
}

// validSelectFlags reports whether CMOVcc encodes the condition: an integer
// condition or a sign flag test, and GP registers.
func validSelectFlags(dst, ifTrue, ifFalse backend.Register, c op.BranchCondition) bool {
	_, ok := conditionCode(c)
	return ok && validGeneralRegisters(dst, ifTrue, ifFalse)
}

// selectFlagsPlan sets dst to ifTrue when the live flags satisfy c and to
// ifFalse otherwise, with a flag-preserving MOV and a 64-bit CMOVcc. When dst
// is ifTrue it moves ifFalse in on the negated condition instead.
func selectFlagsPlan(dst, ifTrue, ifFalse backend.Register, c op.BranchCondition) []expansionStep {
	var x expansion
	d, t, f := uint8(dst), uint8(ifTrue), uint8(ifFalse)
	cc, _ := conditionCode(c)
	switch {
	case d == f:
	case d == t:
		t, cc = f, cc^1
	default:
		x.emit(gpRR(true, f, d, false, 0x89), "mov %s, %s", amd64RegisterNames[d], amd64RegisterNames[f])
	}
	if d != t || d != f {
		x.emit(gpRR(true, d, t, false, 0x0f, 0x40|cc), "cmov%s %s, %s", conditionSuffixes[cc], amd64RegisterNames[d], amd64RegisterNames[t])
	}
	return x.resolve()
}

// SelectFlags sets dst to ifTrue when the flags satisfy condition, else to
// ifFalse. It reads flags and writes none.
func (e *ByteAssembler) SelectFlags(dst, ifTrue, ifFalse backend.Register, condition op.BranchCondition) {
	if !e.require(validSelectFlags(dst, ifTrue, ifFalse, condition)) {
		return
	}
	e.expansion(selectFlagsPlan(dst, ifTrue, ifFalse, condition))
}
func (e *TextAssembler) SelectFlags(dst, ifTrue, ifFalse backend.Register, condition op.BranchCondition) {
	if !e.valid(func(enc *ByteAssembler) { enc.SelectFlags(dst, ifTrue, ifFalse, condition) }) {
		return
	}
	e.expansion(selectFlagsPlan(dst, ifTrue, ifFalse, condition))
}
