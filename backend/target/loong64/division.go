package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Divide expands OpDivide. LA64 division never traps, but the manual leaves
// its result undefined for a zero divisor and for signed MIN / -1, so the
// checked semantics replace those results branch-free with maskeqz/masknez.
// The temporaries are r19 and r20; every operand is read before A is written,
// so A may alias B or C.
func (e *ByteAssembler) Divide(i machine.Instruction) {
	if !e.require(validDivide(i)) {
		return
	}
	w, semantics := i.Bits, op.DivisionSemantics(i.Imm)
	signed := i.Arithmetic == op.OpDivSigned || i.Arithmetic == op.OpRemSigned
	rem := i.Arithmetic == op.OpRem || i.Arithmetic == op.OpRemSigned
	code, name := uint32(0x00230000), "div.du"
	if signed {
		code, name = 0x00220000, "div.d"
	}
	if rem {
		code += 0x8000
		name = "mod" + name[3:]
	}
	d, b, c := i.A, i.B, i.C
	if signed && w == 64 {
		e.divideSigned64(semantics, rem, code, name, d, b, c)
		return
	}
	// Narrow signed operands are widened to 64 bits; the .w forms would need
	// sign-extended inputs as well, and in 64 bits MIN / -1 cannot overflow
	// (masking the quotient to Bits gives MIN, the remainder is 0), so only
	// a zero divisor needs a guard. Unsigned operands arrive zero-extended,
	// so the 64-bit unsigned forms are exact at every width.
	q, t := backend.Register(19), backend.Register(20)
	narrow := signed
	if narrow {
		e.SignExtend(19, b, w)
		e.SignExtend(20, c, w)
		b, c = 19, 20
		// The inputs now live in temporaries, so A is free.
		q, t = d, 19
	}
	switch {
	case semantics == op.DivisionUnchecked:
		e.rrr(code, name, d, b, c)
	case rem:
		// x % 0 = x.
		e.rrr(code, name, q, b, c)
		e.rrr(0x00130000, "maskeqz", q, q, c)
		e.rrr(0x00138000, "masknez", t, b, c)
		e.Binary(op.OpOr, 64, d, q, t)
	case semantics == op.DivisionZero:
		e.rrr(code, name, q, b, c)
		e.rrr(0x00130000, "maskeqz", d, q, c)
	default:
		// x / 0 = all ones: OR in -(c == 0).
		e.rrr(code, name, q, b, c)
		e.ri(0x02400000, "sltui", 20, c, 1)
		e.Binary(op.OpSub, 64, 20, 0, 20)
		e.Binary(op.OpOr, 64, d, q, 20)
	}
	// A signed result has high bits set, and so does an all-ones quotient.
	if w < 64 && (narrow || !rem && semantics == op.DivisionAllOnes) {
		e.Narrow(d, d, w)
	}
}

// divideSigned64 guards 64-bit signed division. Every x / -1 is -x (wrapping
// MIN to MIN) and every x % -1 is 0, so a divisor of -1 selects those
// directly; r20 = C+1 is zero exactly for it.
func (e *ByteAssembler) divideSigned64(semantics op.DivisionSemantics, rem bool, code uint32, name string, d, b, c backend.Register) {
	if semantics == op.DivisionUnchecked {
		e.rrr(code, name, d, b, c)
		return
	}
	if rem {
		e.rrr(code, name, 19, b, c)
		e.ri(0x02c00000, "addi.d", 20, c, 1)
		e.rrr(0x00130000, "maskeqz", 19, 19, 20) // x % -1 = 0
		e.rrr(0x00130000, "maskeqz", 19, 19, c)
		e.rrr(0x00138000, "masknez", 20, b, c) // x % 0 = x
		e.Binary(op.OpOr, 64, d, 19, 20)
		return
	}
	e.ri(0x02c00000, "addi.d", 20, c, 1)
	e.rrr(code, name, 19, b, c)
	e.rrr(0x00130000, "maskeqz", 19, 19, 20)
	e.rrr(0x00138000, "masknez", 20, b, 20)
	e.Binary(op.OpSub, 64, 19, 19, 20) // 0 - x when C == -1
	if semantics == op.DivisionZero {
		e.rrr(0x00130000, "maskeqz", d, 19, c)
		return
	}
	e.ri(0x02400000, "sltui", 20, c, 1)
	e.Binary(op.OpSub, 64, 20, 0, 20)
	e.Binary(op.OpOr, 64, d, 19, 20)
}

// FloatConvert expands OpIntToFloat and OpFloatToInt.
func (e *ByteAssembler) FloatConvert(i machine.Instruction) {
	if !e.require(validConversion(i)) {
		return
	}
	if i.Op == machine.OpIntToFloat {
		e.intToFloat(i)
	} else {
		e.floatToInt(i)
	}
}

// intToFloat converts with ffint, which rounds in the FCSR0 mode. A 32-bit
// unsigned source arrives zero-extended, so it converts exactly as a signed
// 64-bit one. Only A is written in the FP bank.
func (e *ByteAssembler) intToFloat(i machine.Instruction) {
	d := uint32(backend.VectorNumber(i.A))
	code, name := uint32(0x011d1000), "ffint.s."
	if i.Bits == 64 {
		code, name = 0x011d2000, "ffint.d."
	}
	if i.Arithmetic == op.OpFFromSigned && i.Imm == 32 {
		e.floatInput(i.B, d, 32)
		e.floatRR(code, name+"w", d, d)
		return
	}
	code, name = code+0x800, name+"l"
	e.floatInput(i.B, d, 64)
	if i.Arithmetic == op.OpFFromSigned || i.Imm == 32 {
		e.floatRR(code, name, d, d)
		return
	}
	// LA64 has no unsigned conversion. A source with bit 63 set is halved
	// with its lost bit ORed back in: that sticky bit keeps the single
	// ffint rounding correct in every mode, and doubling is exact.
	start := len(e.plan)
	nonnegative := e.forward(0x64000000, "bge", i.B, 0)
	e.shift(op.OpShiftRight, 19, i.B, 1)
	e.ri(0x03400000, "andi", 20, i.B, 1)
	e.Binary(op.OpOr, 64, 19, 19, 20)
	e.floatInput(19, d, 64)
	e.land(start, nonnegative)
	e.floatRR(code, name, d, d)
	exact := e.forward(0x64000000, "bge", i.B, 0)
	operation, add := uint32(0x201<<15), "fadd.s"
	if i.Bits == 64 {
		operation, add = 0x202<<15, "fadd.d"
	}
	e.emit(operation|d<<10|d<<5|d, "%s f%d, f%d, f%d", add, d, d, d)
	e.land(start, exact)
}

// floatToInt converts with ftintrz into the FP temporary f0, the only FP
// register it writes. The manual leaves ftintrz's NaN and out-of-range
// results to the implementation, so ConversionSaturate decodes the exponent
// in r19/r20 first: below 2^limit the hardware result is exact, and above it
// the clamp is built in the GP bank. ConversionNative takes the raw result,
// except that a 64-bit unsigned value at or above 2^63 is still converted.
func (e *ByteAssembler) floatToInt(i machine.Instruction) {
	d, n := i.A, i.Bits
	signed := i.Arithmetic == op.OpFToSigned
	native := op.ConversionSemantics(i.Imm) == op.ConversionNative
	// The 64-bit form also converts every unsigned 32-bit value exactly.
	if native && (signed || n == 32) {
		e.truncate(i, n == 64 || !signed)
		if n == 32 {
			e.Narrow(d, d, 32)
		}
		return
	}
	start := len(e.plan)
	// MOVFR2GR.S sign-extends, so bit 63 is the sign for both widths.
	f := uint32(backend.VectorNumber(i.B))
	fraction, bias := uint32(52), int64(1023)
	if i.Disp == 32 {
		e.emit(0x452d<<10|f<<5|19, "movfr2gr.s r19, f%d", f)
		fraction, bias = 23, 127
	} else {
		e.emit(0x452e<<10|f<<5|19, "movfr2gr.d r19, f%d", f)
	}
	// The exponent field spans bits Disp-2 down to the fraction width.
	top := uint32(i.Disp) - 2
	e.emit(0x00c00000|top<<16|fraction<<10|19<<5|20, "bstrpick.d r20, r19, %d, %d", top, fraction)
	// ftintrz is exact while the unbiased exponent k is below limit.
	limit := int64(n)
	if signed || n == 64 {
		limit--
	}
	e.ri(0x02c00000, "addi.d", 20, 20, -(bias + limit)) // k - limit
	if native {
		// Unsigned 64-bit: a signed conversion covers |f| < 2^63.
		e.truncate(i, true)
		done := e.forward(0x60000000, "blt", 20, 0)
		e.significand(d, i.Disp)
		e.land(start, done)
		return
	}
	high := e.forward(0x64000000, "bge", 20, 0)
	e.truncate(i, n == 64 || !signed)
	if signed {
		if n == 32 {
			e.Narrow(d, d, 32)
		}
		end := e.forward(0x50000000, "b")
		e.land(start, high)
		// MAX, plus one for MIN when negative; NaN gives 0.
		e.nan(top, i.Disp)
		e.shift(op.OpShiftRight, 20, 19, 63)
		e.ri(0x02c00000, "addi.d", d, 0, -1)
		e.shift(op.OpShiftRight, d, d, 65-n)
		e.Binary(op.OpAdd, 64, d, d, 20)
		e.rrr(0x00138000, "masknez", d, d, 12)
		e.land(start, end)
		return
	}
	sign := e.forward(0x50000000, "b")
	e.land(start, high)
	inRange := -1
	if n == 64 {
		// 2^63 <= |f| < 2^64 is an integer: its significand moved into place.
		e.significand(d, i.Disp)
		inRange = e.forward(0x64000000, "bge", 0, 20)
	}
	// All ones, or 0 for NaN.
	e.nan(top, i.Disp)
	e.ri(0x02c00000, "addi.d", d, 0, -1)
	if n == 32 {
		e.shift(op.OpShiftRight, d, d, 32)
	}
	e.rrr(0x00138000, "masknez", d, d, 12)
	e.land(start, sign)
	if inRange >= 0 {
		e.land(start, inRange)
	}
	// A negative value truncates or saturates to 0.
	e.shift(op.OpShiftRight, 20, 19, 63)
	e.rrr(0x00138000, "masknez", d, d, 20)
}

// truncate emits ftintrz to a 32- or 64-bit (wide) integer in f0 and moves
// it to A, sign-extended.
func (e *ByteAssembler) truncate(i machine.Instruction, wide bool) {
	f := uint32(backend.VectorNumber(i.B))
	code, name := uint32(0x011a8400), "ftintrz.w"
	if wide {
		code, name = 0x011aa400, "ftintrz.l"
	}
	if i.Disp == 64 {
		code, name = code+0x400, name+".d"
	} else {
		name += ".s"
	}
	e.floatRR(code, name, 0, f)
	if wide {
		e.emit(0x452e<<10|uint32(i.A), "movfr2gr.d r%d, f0", i.A)
	} else {
		e.emit(0x452d<<10|uint32(i.A), "movfr2gr.s r%d, f0", i.A)
	}
}

// significand writes to d the bit pattern in r19 with the sign and exponent
// replaced by the implicit one and left-aligned at bit 63: the integer value
// of a float whose unbiased exponent is 63.
func (e *ByteAssembler) significand(d backend.Register, width uint64) {
	source := backend.Register(19)
	if width == 32 {
		e.shift(op.OpShiftLeft, d, 19, 52-23)
		source = d
	}
	e.ri(0x03000000, "lu52i.d", d, source, 1)
	e.shift(op.OpShiftLeft, d, d, 11)
}

// nan sets r12 when the float bits in r19 are a NaN: a magnitude above
// infinity's. It clobbers r20.
func (e *ByteAssembler) nan(top uint32, width uint64) {
	e.emit(0x00c00000|top<<16|19<<5|20, "bstrpick.d r20, r19, %d, 0", top)
	if width == 32 {
		e.upper(0x14000000, "lu12i.w", 12, 0x7f800)
	} else {
		e.ri(0x03000000, "lu52i.d", 12, 0, 0x7ff)
	}
	e.rrr(0x00128000, "sltu", 12, 12, 20)
}

// forward emits a branch to a later point of the same expansion, which land
// binds. The offset is fixed by the expansion, so sizing stays exact.
func (e *ByteAssembler) forward(code uint32, name string, regs ...backend.Register) int {
	if len(regs) == 0 {
		e.emit(code, "%s 0x%x", name)
	} else {
		e.emit(code|uint32(regs[0])<<5|uint32(regs[1]), "%s r%d, r%d, 0x%x", name, regs[0], regs[1])
	}
	return len(e.plan) - 1
}

// land points the forward branch at index to the next instruction emitted.
func (e *ByteAssembler) land(start, index int) {
	v := uint32(len(e.plan) - index)
	p := &e.plan[index]
	if p.word>>26 == 0x14 {
		p.word |= (v&0xffff)<<10 | v>>16&0x3ff
	} else {
		p.word |= (v & 0xffff) << 10
	}
	if p.text != nil {
		p.text.args = append(p.text.args, e.pc+4*(len(e.plan)-start))
	}
}
