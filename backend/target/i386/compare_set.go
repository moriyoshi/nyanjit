package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// integerConditionCodes are the condition codes (the low nibble of Jcc,
// SETcc and CMOVcc) of the integer branch conditions and the sign flags,
// indexed by op.BranchCondition.
var integerConditionCodes = [...]byte{0x4, 0x5, 0xc, 0xd, 0xe, 0xf, 0x2, 0x3, 0x6, 0x7, 0x8, 0x9}

// conditionSuffixes name the condition codes in mnemonics.
var conditionSuffixes = [16]string{"o", "no", "b", "ae", "e", "ne", "be", "a", "s", "ns", "p", "np", "l", "ge", "le", "g"}

var byteNames = [...]string{"AL", "CL", "DL", "BL"}

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

// validCompareSet reports whether the operands fit: GP registers, a defined
// integer condition and Bits 8, 16 or 32. A signed 8-bit compare reads byte
// registers, so its operands must be EAX..EBX.
func validCompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) bool {
	signedByte := w == 8 && machine.ComparisonFor(c) == machine.CompareSigned
	return c <= op.BranchAbove && (w == 8 || w == 16 || w == 32) && validGeneralRegisters(d, a, b) && (!signedByte || a < 4 && b < 4)
}

// compareSetPlan compares B with C, at 32 bits for equality, unsigned and
// 32-bit conditions on the zero-extended operands, and at the operand width
// for signed narrow ones. EAX..EBX then take SETcc and MOVZX; ESP..EDI have
// no byte register and take MOV A, 0 (which keeps the flags), the inverse
// branch and INC A. A may alias B or C, which are read first.
func compareSetPlan(c op.BranchCondition, w uint8, d, a, b backend.Register, text bool) []expansionStep {
	x := &expansion{text: text}
	left, right := byte(a), byte(b)
	switch {
	case machine.ComparisonFor(c) != machine.CompareSigned || w == 32:
		x.alu(aluCmp, a, b)
	case w == 16:
		x.emit([]byte{0x66, 0x39, modRM(3, right, left)}, "cmp %s, %s", name(a)[1:], name(b)[1:])
	default:
		x.emit([]byte{0x38, modRM(3, right, left)}, "cmp %s, %s", byteNames[a], byteNames[b])
	}
	cc := integerConditionCodes[c]
	r := byte(d)
	if d < 4 {
		x.emit([]byte{0x0f, 0x90 | cc, modRM(3, 0, r)}, "set%s %s", conditionSuffixes[cc], byteNames[d])
		x.emit([]byte{0x0f, 0xb6, modRM(3, r, r)}, "movzx %s, %s", name(d), byteNames[d])
		return x.resolve()
	}
	skip := x.label()
	x.emit(append32([]byte{0xb8 | r}, 0), "mov %s, 0", name(d))
	x.branch(cc^1, skip)
	x.emit([]byte{0x40 | r}, "inc %s", name(d))
	x.bind(skip)
	return x.resolve()
}

// CompareSet sets GP register d to 1 when the w-bit integers in a and b
// satisfy c, else to 0. It writes flags.
func (e *ByteAssembler) CompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validCompareSet(c, w, d, a, b)) {
		return
	}
	emitExpansion(e, compareSetPlan(c, w, d, a, b, false))
}
func (e *TextAssembler) CompareSet(c op.BranchCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validCompareSet(c, w, d, a, b)) {
		return
	}
	recordExpansion(e, compareSetPlan(c, w, d, a, b, true))
}

// validSelectFlags reports whether CMOVcc encodes the condition: an integer
// condition or a sign flag test, and GP registers.
func validSelectFlags(dst, ifTrue, ifFalse backend.Register, c op.BranchCondition) bool {
	_, ok := conditionCode(c)
	return ok && validGeneralRegisters(dst, ifTrue, ifFalse)
}

// selectFlagsPlan sets dst to ifTrue when the live flags satisfy c and to
// ifFalse otherwise, with a flag-preserving MOV and CMOVcc. When dst is
// ifTrue it moves ifFalse in on the negated condition instead.
func selectFlagsPlan(dst, ifTrue, ifFalse backend.Register, c op.BranchCondition, text bool) []expansionStep {
	x := &expansion{text: text}
	cc, _ := conditionCode(c)
	switch {
	case dst == ifFalse:
	case dst == ifTrue:
		ifTrue, cc = ifFalse, cc^1
	default:
		x.mov(dst, ifFalse)
	}
	if dst != ifTrue || dst != ifFalse {
		x.emit([]byte{0x0f, 0x40 | cc, modRM(3, byte(dst), byte(ifTrue))}, "cmov%s %s, %s", conditionSuffixes[cc], name(dst), name(ifTrue))
	}
	return x.resolve()
}

// SelectFlags sets dst to ifTrue when the flags satisfy condition, else to
// ifFalse. It reads flags and writes none.
func (e *ByteAssembler) SelectFlags(dst, ifTrue, ifFalse backend.Register, condition op.BranchCondition) {
	if !e.require(validSelectFlags(dst, ifTrue, ifFalse, condition)) {
		return
	}
	emitExpansion(e, selectFlagsPlan(dst, ifTrue, ifFalse, condition, false))
}
func (e *TextAssembler) SelectFlags(dst, ifTrue, ifFalse backend.Register, condition op.BranchCondition) {
	if !e.require(validSelectFlags(dst, ifTrue, ifFalse, condition)) {
		return
	}
	recordExpansion(e, selectFlagsPlan(dst, ifTrue, ifFalse, condition, true))
}
