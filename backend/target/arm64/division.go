package arm64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// validDivide checks an OpDivide. All three operands are GP registers.
func validDivide(ins *machine.Instruction) bool {
	if ins.Op != machine.OpDivide || !validInstructionRegisters(ins) || !validWidth(ins.Bits) || !op.DivisionSemantics(ins.Imm).Valid() || ins.Imm > 0xff {
		return false
	}
	switch ins.Arithmetic {
	case op.OpDiv, op.OpDivSigned, op.OpRem, op.OpRemSigned:
		return true
	}
	return false
}

// divisionPlan expands OpDivide. UDIV and SDIV already give DivisionZero's
// results: x/0 = 0 and MIN/-1 = MIN. A remainder is x - q*y (MSUB), which
// then gives x%0 = x and MIN%-1 = 0, so only an all-ones quotient needs a
// fixup: CMP y before the division (A may alias C) and CSINV after it.
// DivisionUnchecked is the same sequence, since nothing here can trap.
//
// Narrow signed operands are sign-extended into X16 and X17 for a 32-bit
// SDIV; the quotient may then go straight to A because the inputs no longer
// live in B or C. Other remainders keep the quotient in X16 so MSUB still
// reads the original B and C when A aliases one of them. W forms zero-extend
// a 32-bit result; narrow results are zero-extended to Bits explicitly.
func divisionPlan(ins machine.Instruction, text bool) concretePlan {
	var out concretePlan
	if !validDivide(&ins) {
		return out
	}
	// Listing text is formatted under a text check at each call so a size
	// or byte query boxes no arguments.
	emit := func(word uint32) { out.parts[out.n].word = word; out.n++ }
	listing := func(format string, args ...any) { out.parts[out.n-1].text = fmt.Sprintf(format, args...) }
	w := ins.Bits
	d, x, y := ins.A, ins.B, ins.C
	sf, r := uint32(1)<<31, "X"
	if w <= 32 {
		sf, r = 0, "W"
	}
	signed := ins.Arithmetic == op.OpDivSigned || ins.Arithmetic == op.OpRemSigned
	remainder := ins.Arithmetic == op.OpRem || ins.Arithmetic == op.OpRemSigned
	allOnes := !remainder && op.DivisionSemantics(ins.Imm) == op.DivisionAllOnes
	if allOnes {
		emit(0x7100001f | sf | uint32(y)<<5)
		if text {
			listing("cmp %s%d, #0", r, y)
		}
	}
	extend := signed && w < 32
	if extend {
		emit(0x13000000 | uint32(w-1)<<10 | uint32(x)<<5 | 16)
		if text {
			listing("sxt%s W16, W%d", narrowSuffix(w), x)
		}
		emit(0x13000000 | uint32(w-1)<<10 | uint32(y)<<5 | 17)
		if text {
			listing("sxt%s W17, W%d", narrowSuffix(w), y)
		}
		x, y = 16, 17
	}
	q := d
	if remainder && !extend {
		q = 16
	}
	divide, name := uint32(0x1ac00800), "udiv"
	if signed {
		divide, name = 0x1ac00c00, "sdiv"
	}
	emit(divide | sf | uint32(y)<<16 | uint32(x)<<5 | uint32(q))
	if text {
		listing("%s %s%d, %s%d, %s%d", name, r, q, r, x, r, y)
	}
	if remainder {
		emit(0x1b008000 | sf | uint32(y)<<16 | uint32(x)<<10 | uint32(q)<<5 | uint32(d))
		if text {
			listing("msub %s%d, %s%d, %s%d, %s%d", r, d, r, q, r, y, r, x)
		}
	}
	if allOnes {
		// CSINV d, d, ZR, NE leaves the quotient for a nonzero divisor and
		// writes ~0 otherwise; narrowing below trims it to Bits.
		emit(0x5a800000 | sf | 31<<16 | 1<<12 | uint32(d)<<5 | uint32(d))
		if text {
			listing("csinv %s%d, %s%d, %sZR, ne", r, d, r, d, r)
		}
	}
	if w < 32 && (extend || allOnes) {
		emit(0x53000000 | uint32(w-1)<<10 | uint32(d)<<5 | uint32(d))
		if text {
			listing("uxt%s W%d, W%d", narrowSuffix(w), d, d)
		}
	}
	return out
}

func narrowSuffix(w uint8) string {
	if w == 8 {
		return "b"
	}
	return "h"
}

// divisionWritesFlags reports whether the expansion has the CMP for an
// all-ones quotient.
func divisionWritesFlags(ins machine.Instruction) bool {
	return ins.Op == machine.OpDivide && (ins.Arithmetic == op.OpDiv || ins.Arithmetic == op.OpDivSigned) &&
		op.DivisionSemantics(ins.Imm) == op.DivisionAllOnes
}

// Divide implements OpDivide.
func (e *ByteAssembler) Divide(ins machine.Instruction) {
	if !e.require(validDivide(&ins)) {
		return
	}
	p := divisionPlan(ins, false)
	for _, i := range p.parts[:p.n] {
		e.words = append(e.words, i.word)
	}
}

// Divide renders exactly the plan used by native emission.
func (e *TextAssembler) Divide(ins machine.Instruction) {
	if !e.valid(func(enc *ByteAssembler) { enc.Divide(ins) }) {
		return
	}
	p := divisionPlan(ins, true)
	for _, i := range p.parts[:p.n] {
		e.line("%s", i.text)
	}
}

// validFloatConvert checks an OpIntToFloat or OpFloatToInt: the integer
// operand is GP and the float operand is an FP register, never a GP image.
func validFloatConvert(ins *machine.Instruction) bool {
	if !ins.ByteOrder.Valid() || ins.Bits != 32 && ins.Bits != 64 {
		return false
	}
	switch ins.Op {
	case machine.OpIntToFloat:
		return validVectorRegister(ins.A) && ins.B < 32 && (ins.Imm == 32 || ins.Imm == 64) &&
			(ins.Arithmetic == op.OpFFromSigned || ins.Arithmetic == op.OpFFromUnsigned)
	case machine.OpFloatToInt:
		return ins.A < 32 && validVectorRegister(ins.B) && (ins.Disp == 32 || ins.Disp == 64) &&
			op.ConversionSemantics(ins.Imm).Valid() && ins.Imm <= 0xff &&
			(ins.Arithmetic == op.OpFToSigned || ins.Arithmetic == op.OpFToUnsigned)
	}
	return false
}

// floatConvertWord encodes the single conversion instruction. SCVTF and UCVTF
// round in the FPCR mode, and a W source covers a zero-extended 32-bit
// integer of either signedness. FCVTZS and FCVTZU truncate, saturate to the
// destination range and give 0 for NaN, which is ConversionSaturate exactly,
// so ConversionNative uses the same instruction. A W destination is
// zero-extended.
func floatConvertWord(ins machine.Instruction) uint32 {
	if ins.Op == machine.OpIntToFloat {
		word := uint32(0x1e220000) // SCVTF Sd, Wn
		if ins.Arithmetic == op.OpFFromUnsigned {
			word = 0x1e230000 // UCVTF Sd, Wn
		}
		if ins.Imm == 64 {
			word |= 1 << 31
		}
		if ins.Bits == 64 {
			word |= 1 << 22
		}
		return word | uint32(ins.B)<<5 | uint32(backend.VectorNumber(ins.A))
	}
	word := uint32(0x1e380000) // FCVTZS Wd, Sn
	if ins.Arithmetic == op.OpFToUnsigned {
		word = 0x1e390000 // FCVTZU Wd, Sn
	}
	if ins.Bits == 64 {
		word |= 1 << 31
	}
	if ins.Disp == 64 {
		word |= 1 << 22
	}
	return word | uint32(backend.VectorNumber(ins.B))<<5 | uint32(ins.A)
}

// floatConvertText lists the instruction floatConvertWord encodes.
func floatConvertText(ins machine.Instruction) string {
	fpr := func(w uint64) string {
		if w == 64 {
			return "D"
		}
		return "S"
	}
	gpr := func(w uint64) string {
		if w == 64 {
			return "X"
		}
		return "W"
	}
	if ins.Op == machine.OpIntToFloat {
		name := "scvtf"
		if ins.Arithmetic == op.OpFFromUnsigned {
			name = "ucvtf"
		}
		return fmt.Sprintf("%s %s%d, %s%d", name, fpr(uint64(ins.Bits)), backend.VectorNumber(ins.A), gpr(ins.Imm), ins.B)
	}
	name := "fcvtzs"
	if ins.Arithmetic == op.OpFToUnsigned {
		name = "fcvtzu"
	}
	return fmt.Sprintf("%s %s%d, %s%d", name, gpr(uint64(ins.Bits)), ins.A, fpr(ins.Disp), backend.VectorNumber(ins.B))
}

// FloatConvert implements OpIntToFloat and OpFloatToInt.
func (e *ByteAssembler) FloatConvert(ins machine.Instruction) {
	if !e.require(validFloatConvert(&ins)) {
		return
	}
	e.words = append(e.words, floatConvertWord(ins))
}

// FloatConvert renders the single conversion instruction.
func (e *TextAssembler) FloatConvert(ins machine.Instruction) {
	if !e.valid(func(enc *ByteAssembler) { enc.FloatConvert(ins) }) {
		return
	}
	e.line("%s", floatConvertText(ins))
}
