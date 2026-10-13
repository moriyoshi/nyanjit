package amd64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// floatCompareForm is the flag test that decides a float condition after
// UCOMISx X, Y, which sets ZF=PF=CF=1 when unordered, CF for X<Y, ZF for
// X==Y and none for X>Y. swap compares C with B instead of B with C. Equal
// and NotEqual need two flags: Equal is (CF|ZF)-CF, SETBE then SBB A, 0,
// and NotEqual is (!CF&!ZF)+CF, SETA then ADC A, 0; carry decides
// unordered and less alike, and either term alone is 0 or 1.
type floatCompareForm struct {
	swap bool
	cc   byte // condition code of SETcc
	fix  byte // ModRM digit of the 83 /digit ib carry fixup; 0 for none
}

const (
	ccBelow      byte = 0x2
	ccAboveEqual byte = 0x3
	ccEqual      byte = 0x4
	ccNotEqual   byte = 0x5
	ccBelowEqual byte = 0x6
	ccAbove      byte = 0x7
	ccParity     byte = 0xa
	ccNoParity   byte = 0xb

	fixAdc byte = 2
	fixSbb byte = 3
)

var floatCompareForms = [...]floatCompareForm{
	op.FloatEqual:                 {false, ccBelowEqual, fixSbb},
	op.FloatNotEqual:              {false, ccAbove, fixAdc},
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

var setMnemonics = map[byte]string{
	ccBelow: "setb", ccAboveEqual: "setae", ccEqual: "sete", ccNotEqual: "setne",
	ccBelowEqual: "setbe", ccAbove: "seta", ccParity: "setp", ccNoParity: "setnp",
}

// validFloatCompare reports whether the operands fit: a GP result and two
// XMM inputs, Bits 32 or 64 and a defined condition.
func validFloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) bool {
	return int(c) < len(floatCompareForms) && (w == 32 || w == 64) &&
		validGeneralRegisters(d) && validVectorRegister(a) && validVectorRegister(b)
}

// floatComparePlan sets A to the condition. A is a GP register, so it never
// aliases the inputs, and is cleared before UCOMISx so that SETcc, which
// writes only A's low byte, leaves it zero-extended. No other register is
// written.
func floatComparePlan(c op.FloatCondition, w uint8, d, a, b backend.Register) []expansionStep {
	var x expansion
	f := floatCompareForms[c]
	r, left, right := uint8(d), backend.VectorNumber(a), backend.VectorNumber(b)
	if f.swap {
		left, right = right, left
	}
	prefix, name := byte(0), "ucomiss"
	if w == 64 {
		prefix, name = 0x66, "ucomisd"
	}
	a32 := amd64RegisterNames32[r]
	x.emit(gpRR(false, r, r, false, 0x31), "xor %s, %s", a32, a32)
	x.emit(sseRR(prefix, false, 0x2e, left, right), "%s xmm%d, xmm%d", name, left, right)
	x.emit(gpRR(false, 0, r, true, 0x0f, 0x90|f.cc), "%s %s", setMnemonics[f.cc], amd64RegisterNames8[r])
	switch f.fix {
	case fixAdc:
		x.emit(append(gpRR(false, fixAdc, r, false, 0x83), 0), "adc %s, 0", a32)
	case fixSbb:
		x.emit(append(gpRR(false, fixSbb, r, false, 0x83), 0), "sbb %s, 0", a32)
	}
	return x.resolve()
}

// FloatCompare sets GP register d to 1 when the w-bit floats in XMM
// registers a and b satisfy c, else to 0. It writes flags.
func (e *ByteAssembler) FloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validFloatCompare(c, w, d, a, b)) {
		return
	}
	e.expansion(floatComparePlan(c, w, d, a, b))
}
func (e *TextAssembler) FloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.FloatCompare(c, w, d, a, b) }) {
		return
	}
	e.expansion(floatComparePlan(c, w, d, a, b))
}
