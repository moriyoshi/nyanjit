package arm64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// AArch64 condition codes, numbered as in the cond field.
const (
	condEQ uint32 = 0
	condNE uint32 = 1
	condMI uint32 = 4
	condPL uint32 = 5
	condVS uint32 = 6
	condVC uint32 = 7
	condHI uint32 = 8
	condLS uint32 = 9
	condGE uint32 = 10
	condLT uint32 = 11
	condGT uint32 = 12
	condLE uint32 = 13
)

var conditionMnemonics = [...]string{"eq", "ne", "hs", "lo", "mi", "pl", "vs", "vc", "hi", "ls", "ge", "lt", "gt", "le"}

// floatConditionCodes maps each single-instruction op.FloatCondition to the
// condition that holds after FCMP, which sets NZCV to 0110 when equal, 1000
// when less, 0010 when greater and 0011 when unordered. Each pair keeps its
// negation: c^1 maps to cond^1.
var floatConditionCodes = [...]uint32{
	op.FloatEqual:                 condEQ, // Z
	op.FloatNotEqual:              condNE, // !Z: less, greater, unordered
	op.FloatLess:                  condMI, // N: less only
	op.FloatUnorderedGreaterEqual: condPL, // !N
	op.FloatLessEqual:             condLS, // !C || Z: less, equal
	op.FloatUnorderedGreater:      condHI, // C && !Z: greater, unordered
	op.FloatGreater:               condGT, // !Z && N==V: greater only
	op.FloatUnorderedLessEqual:    condLE, // Z || N!=V
	op.FloatGreaterEqual:          condGE, // N==V: equal, greater
	op.FloatUnorderedLess:         condLT, // N!=V: less, unordered
	op.FloatOrdered:               condVC, // !V
	op.FloatUnordered:             condVS, // V
}

// validFloatCompareOperands checks an OpFloatCompare: a GP destination, two
// FP operands, a 32- or 64-bit width and a defined condition.
func validFloatCompareOperands(c op.FloatCondition, w uint8, d, a, b backend.Register) bool {
	return validGeneralRegisters(d) && validVectorRegister(a) && validVectorRegister(b) && (w == 32 || w == 64) &&
		c <= op.FloatUnorderedEqual
}

// floatCompareWords encodes FCMP of a and b, then CSET of the condition into
// W d, which zero-extends into X d. The two conditions no single code covers
// are a CSET and a CSINC that keeps W d or gives WZR+1:
//
//	OrderedNotEqual: cset Wd, mi; csinc Wd, Wd, wzr, le  (greater gives 1)
//	UnorderedEqual:  cset Wd, eq; csinc Wd, Wd, wzr, vc  (unordered gives 1)
func floatCompareWords(c op.FloatCondition, w uint8, d, a, b backend.Register) []uint32 {
	fcmp := uint32(0x1e202000) | uint32(backend.VectorNumber(b))<<16 | uint32(backend.VectorNumber(a))<<5
	if w == 64 {
		fcmp |= 1 << 22
	}
	// CSINC Wd, Wn, Wm, cond; CSET Wd, cond is CSINC Wd, WZR, WZR, !cond.
	csinc := func(n, m backend.Register, cond uint32) uint32 {
		return 0x1a800400 | uint32(m)<<16 | cond<<12 | uint32(n)<<5 | uint32(d)
	}
	cset := func(cond uint32) uint32 { return csinc(31, 31, cond^1) }
	switch c {
	case op.FloatOrderedNotEqual:
		return []uint32{fcmp, cset(condMI), csinc(d, 31, condLE)}
	case op.FloatUnorderedEqual:
		return []uint32{fcmp, cset(condEQ), csinc(d, 31, condVC)}
	}
	return []uint32{fcmp, cset(floatConditionCodes[c])}
}

// floatCompareSize is the byte size of a valid OpFloatCompare.
func floatCompareSize(c op.FloatCondition) int {
	if c == op.FloatOrderedNotEqual || c == op.FloatUnorderedEqual {
		return 12
	}
	return 8
}

// validFloatCompare checks an OpFloatCompare instruction.
func validFloatCompare(ins *machine.Instruction) bool {
	return ins.ByteOrder.Valid() && ins.Imm <= uint64(op.FloatUnorderedEqual) &&
		validFloatCompareOperands(op.FloatCondition(ins.Imm), ins.Bits, ins.A, ins.B, ins.C)
}

// FloatCompare implements OpFloatCompare: W d is 1 when the w-bit floats in
// FP registers a and b satisfy c, else 0. It writes NZCV.
func (e *ByteAssembler) FloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) {
	if !e.require(validFloatCompareOperands(c, w, d, a, b)) {
		return
	}
	e.words = append(e.words, floatCompareWords(c, w, d, a, b)...)
}

// FloatCompare renders the sequence floatCompareWords encodes.
func (e *TextAssembler) FloatCompare(c op.FloatCondition, w uint8, d, a, b backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.FloatCompare(c, w, d, a, b) }) {
		return
	}
	fpr := "S"
	if w == 64 {
		fpr = "D"
	}
	e.line("fcmp %s%d, %s%d", fpr, backend.VectorNumber(a), fpr, backend.VectorNumber(b))
	switch c {
	case op.FloatOrderedNotEqual:
		e.line("cset W%d, mi", d)
		e.line("csinc W%d, W%d, WZR, le", d, d)
	case op.FloatUnorderedEqual:
		e.line("cset W%d, eq", d)
		e.line("csinc W%d, W%d, WZR, vc", d, d)
	default:
		e.line("cset W%d, %s", d, conditionMnemonics[floatConditionCodes[c]])
	}
}
