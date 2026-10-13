package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// floatCompareForm is the flag test that decides a float condition after
// UCOMISx X, Y, which sets ZF=PF=CF=1 when unordered, CF for X<Y, ZF for
// X==Y and none for X>Y. swap compares C with B instead of B with C. Equal
// and NotEqual need two flags: Equal is (CF|ZF)-CF, the BE test then
// SBB A, 0, and NotEqual is (!CF&!ZF)+CF, the A test then ADC A, 0; carry
// decides unordered and less alike, and either term alone is 0 or 1.
type floatCompareForm struct {
	swap bool
	cc   byte // condition code of the test
	fix  byte // groupAdc or groupSbb digit of the carry fixup; 0 for none
}

var floatCompareForms = [...]floatCompareForm{
	op.FloatEqual:                 {false, ccBelowEqual, groupSbb},
	op.FloatNotEqual:              {false, ccAbove, groupAdc},
	op.FloatLess:                  {true, ccAbove, 0},
	op.FloatUnorderedGreaterEqual: {true, ccBelowEqual, 0},
	op.FloatLessEqual:             {true, ccAboveEqual, 0},
	op.FloatUnorderedGreater:      {true, ccBelow, 0},
	op.FloatGreater:               {false, ccAbove, 0},
	op.FloatUnorderedLessEqual:    {false, ccBelowEqual, 0},
	op.FloatGreaterEqual:          {false, ccAboveEqual, 0},
	op.FloatUnorderedLess:         {false, ccBelow, 0},
	op.FloatOrdered:               {false, ccNoParity, 0},
	op.FloatUnordered:             {false, ccParity, 0},
	op.FloatOrderedNotEqual:       {false, ccNotEqual, 0},
	op.FloatUnorderedEqual:        {false, ccEqual, 0},
}

// validFloatCompare reports whether the operands fit: a GP result and two
// XMM inputs, Bits 32 or 64 and a defined condition.
func validFloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) bool {
	xmm := func(r backend.Register) bool { return backend.IsVectorRegister(r) && backend.VectorNumber(r) < 8 }
	return int(c) < len(floatCompareForms) && (w == 32 || w == 64) && validGeneralRegisters(d) && xmm(a) && xmm(b)
}

// floatComparePlan sets A to the condition; A is a GP register, so it never
// aliases the inputs, and no other register is written. SETcc reaches only
// AL..BL, so A is cleared before UCOMISx and set with SETcc for EAX..EBX.
// ESP..EDI take the carry alone from SBB A, A (NEG for 1 on carry, INC for
// 1 on no carry), or else stand the test in for SETcc with a branch over
// INC A, which keeps CF for the Equal and NotEqual fixups.
func floatComparePlan(c op.FloatCondition, w uint8, d, a, b backend.Register, text bool) []expansionStep {
	x := &expansion{text: text}
	f := floatCompareForms[c]
	left, right := byte(backend.VectorNumber(a)), byte(backend.VectorNumber(b))
	if f.swap {
		left, right = right, left
	}
	compare := func() {
		if w == 64 {
			x.sse(0x66, 0x2e, left, right, "ucomisd xmm%d, xmm%d", left, right)
		} else {
			x.sse(0, 0x2e, left, right, "ucomiss xmm%d, xmm%d", left, right)
		}
	}
	r := byte(d)
	switch {
	case d < 4:
		x.alu(aluXor, d, d)
		compare()
		x.emit([]byte{0x0f, 0x90 | f.cc, modRM(3, 0, r)}, "set%s %s", conditionMnemonics[f.cc][1:], [...]string{"AL", "CL", "DL", "BL"}[d])
	case f.cc == ccBelow:
		compare()
		x.alu(aluSbb, d, d)
		x.unary(unaryNeg, d)
		return x.resolve()
	case f.cc == ccAboveEqual:
		compare()
		x.alu(aluSbb, d, d)
		x.emit([]byte{0x40 | r}, "inc %s", name(d))
		return x.resolve()
	default:
		skip := x.label()
		x.alu(aluXor, d, d)
		compare()
		x.branch(f.cc^1, skip)
		x.emit([]byte{0x40 | r}, "inc %s", name(d))
		x.bind(skip)
	}
	if f.fix != 0 {
		x.immediate(f.fix, d, 0)
	}
	return x.resolve()
}

func (e *ByteAssembler) FloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validFloatCompare(c, w, d, a, b)) {
		return
	}
	emitExpansion(e, floatComparePlan(c, w, d, a, b, false))
}
func (e *TextAssembler) FloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validFloatCompare(c, w, d, a, b)) {
		return
	}
	recordExpansion(e, floatComparePlan(c, w, d, a, b, true))
}
