package amd64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// sseRR encodes an SSE register-register operation; prefix 0 means none.
func sseRR(prefix byte, wide bool, opcode byte, reg, rm uint8) []byte {
	code := gpRR(wide, reg, rm, false, 0x0f, opcode)
	if prefix == 0 {
		return code
	}
	return append([]byte{prefix}, code...)
}

func validFloatConvert(i machine.Instruction) bool {
	switch i.Op {
	case machine.OpIntToFloat:
		// The unsigned 64-bit sequence forms its halved operand in R11.
		return validVectorRegister(i.A) && validGeneralRegisters(i.B) && (i.Bits == 32 || i.Bits == 64) && (i.Imm == 32 || i.Imm == 64) &&
			(i.Arithmetic == op.OpFFromSigned || i.Arithmetic == op.OpFFromUnsigned && !(i.Imm == 64 && i.B == 11))
	case machine.OpFloatToInt:
		return validGeneralRegisters(i.A) && validVectorRegister(i.B) && (i.Bits == 32 || i.Bits == 64) && (i.Disp == 32 || i.Disp == 64) &&
			(i.Arithmetic == op.OpFToSigned || i.Arithmetic == op.OpFToUnsigned) && i.Imm <= 0xff && op.ConversionSemantics(i.Imm).Valid()
	}
	return false
}

// convertWritesFlags reports whether a conversion's expansion writes flags:
// CVTSI2Sx and CVTTSx2SI do not, the range checks around them do.
func convertWritesFlags(i machine.Instruction) bool {
	if i.Op == machine.OpIntToFloat {
		return i.Arithmetic == op.OpFFromUnsigned && i.Imm == 64
	}
	return i.Arithmetic == op.OpFToUnsigned || op.ConversionSemantics(i.Imm) == op.ConversionSaturate
}

// intToFloatPlan converts with CVTSI2SD/SS, which only knows signed sources.
// A zero-extended unsigned 32-bit source converts exactly as a 64-bit one.
// An unsigned 64-bit source with the top bit set is halved into R11 with its
// low bit ORed back in as a sticky bit, converted and doubled: the sticky
// bit lies below the rounding position, so the halved conversion rounds as
// the full value would in every rounding mode. Only A is written among the
// FP registers.
func intToFloatPlan(i machine.Instruction) []expansionStep {
	var x expansion
	a, b := backend.VectorNumber(i.A), uint8(i.B)
	prefix, suffix := byte(0xf3), "ss"
	if i.Bits == 64 {
		prefix, suffix = 0xf2, "sd"
	}
	// CVTSI2Sx merges into A; clearing A first breaks the dependency on it.
	x.emit(sseRR(0, false, 0x57, a, a), "xorps xmm%d, xmm%d", a, a)
	convert := func(src uint8, wide bool) {
		x.emit(sseRR(prefix, wide, 0x2a, a, src), "cvtsi2%s xmm%d, %s", suffix, a, gpNames(wide)(src))
	}
	unsigned := i.Arithmetic == op.OpFFromUnsigned
	if !unsigned || i.Imm == 32 {
		convert(b, i.Imm == 64 || unsigned)
		return x.resolve()
	}
	big, done := x.label(), x.label()
	x.emit(gpRR(true, b, b, false, 0x85), "test %s, %s", amd64Name(i.B), amd64Name(i.B))
	x.branch(0x78, "js", big)
	convert(b, true)
	x.branch(0xeb, "jmp", done)
	x.bind(big)
	// R11 = (B | (B&1)<<1) >> 1 = B>>1 | B&1.
	x.emit(gpRR(false, b, 11, false, 0x89), "mov R11D, %s", amd64RegisterNames32[b])
	x.emit(append(gpRR(false, 4, 11, false, 0x83), 1), "and R11D, 1")
	x.emit(gpRR(false, 11, 11, false, 0x01), "add R11D, R11D")
	x.emit(gpRR(true, b, 11, false, 0x09), "or R11, %s", amd64Name(i.B))
	x.emit(gpRR(true, 5, 11, false, 0xd1), "shr R11, 1")
	convert(11, true)
	x.emit(sseRR(prefix, false, 0x58, a, a), "add%s xmm%d, xmm%d", suffix, a, a)
	x.bind(done)
	return x.resolve()
}

// floatToIntPlan converts with CVTTSD/SS2SI, whose out-of-range and NaN
// result is the integer indefinite MIN. Range checks read B with UCOMISx
// (NaN) and by moving its bits into A, which is free once the conversion's
// value is known to be unusable; no other register is written.
func floatToIntPlan(i machine.Instruction) []expansionStep {
	var x expansion
	a, b := uint8(i.A), backend.VectorNumber(i.B)
	in, w := uint8(i.Disp), i.Bits
	unsigned := i.Arithmetic == op.OpFToUnsigned
	saturate := op.ConversionSemantics(i.Imm) == op.ConversionSaturate
	prefix, suffix := byte(0xf3), "ss"
	compare, compareName := byte(0), "ucomiss"
	if in == 64 {
		prefix, suffix = 0xf2, "sd"
		compare, compareName = 0x66, "ucomisd"
	}
	a32, a64 := amd64RegisterNames32[a], amd64RegisterNames[a]
	// An unsigned 32-bit result comes from the 64-bit conversion, which is
	// exact over the whole unsigned 32-bit range.
	wideConvert := w == 64 || unsigned
	convert := func() {
		x.emit(sseRR(prefix, wideConvert, 0x2c, a, b), "cvtt%s2si %s, xmm%d", suffix, gpNames(wideConvert)(a), b)
	}
	nan := func(label int) {
		x.emit(sseRR(compare, false, 0x2e, b, b), "%s xmm%d, xmm%d", compareName, b, b)
		x.branch(0x7a, "jp", label)
	}
	bits := func() {
		if in == 64 {
			x.emit(appendFPTransfer(nil, 64, false, b, a), "movq %s, xmm%d", a64, b)
		} else {
			x.emit(appendFPTransfer(nil, 32, false, b, a), "movd %s, xmm%d", a32, b)
		}
	}
	shift := func(wide bool, digit, count byte, mnemonic string) {
		x.emit(append(gpRR(wide, digit, a, false, 0xc1), count), "%s %s, %d", mnemonic, gpNames(wide)(a), count)
	}
	// sign sets A to all ones for a negative B and to zero otherwise, in the
	// low 32 bits only unless full.
	sign := func(full bool) {
		bits()
		switch {
		case in == 64:
			shift(true, 7, 63, "sar")
		case full:
			shift(true, 4, 32, "shl")
			shift(true, 7, 63, "sar")
		default:
			shift(false, 7, 31, "sar")
		}
	}
	xorSelf := func() { x.emit(gpRR(false, a, a, false, 0x31), "xor %s, %s", a32, a32) }
	zero, done := x.label(), x.label()
	convert()
	switch {
	case !unsigned && !saturate:
	case !unsigned:
		// Only MIN can be the indefinite value; A - 1 overflows only for it.
		x.emit(append(gpRR(w == 64, 7, a, false, 0x83), 1), "cmp %s, 1", gpNames(w == 64)(a))
		x.branch(0x71, "jno", done)
		nan(zero)
		if w == 64 {
			sign(true)
			x.emit(gpRR(true, 2, a, false, 0xf7), "not %s", a64)
			x.emit(append(gpRR(true, 7, a, false, 0x0f, 0xba), 63), "btc %s, 63", a64)
		} else {
			sign(false)
			x.emit(append(gpRR(false, 6, a, false, 0x81), 0xff, 0xff, 0xff, 0x7f), "xor %s, 0x7fffffff", a32)
		}
		x.branch(0xeb, "jmp", done)
	case w == 32 && !saturate:
		x.emit(gpRR(false, a, a, false, 0x89), "mov %s, %s", a32, a32)
	case w == 32:
		// In range exactly when the 64-bit result's high half is zero; then
		// convert again rather than spend a temporary on the value. Out of
		// range, B is too large (MAX) unless it is negative or NaN (0).
		out := x.label()
		shift(true, 5, 32, "shr")
		x.branch(0x75, "jnz", out)
		convert()
		x.branch(0xeb, "jmp", done)
		x.bind(out)
		nan(zero)
		sign(false)
		x.emit(gpRR(false, 2, a, false, 0xf7), "not %s", a32)
		x.branch(0xeb, "jmp", done)
	default:
		// A negative result is the indefinite value or a negative B. B in
		// [2^63, 2^64) is an integer whose bits give the value directly:
		// the mantissa with its implicit one moved up to bit 63.
		exponent, threshold, fraction, mantissa := uint32(0x43e), uint32(0x800), byte(52), byte(11)
		if in == 32 {
			exponent, threshold, fraction, mantissa = 0xbe, 0x100, 23, 40
		}
		x.emit(gpRR(true, a, a, false, 0x85), "test %s, %s", a64, a64)
		x.branch(0x79, "jns", done)
		other := x.label()
		if saturate {
			// The sign and biased exponent of a B in [2^63, 2^64) are exactly
			// exponent; other values above threshold are negative.
			bits()
			shift(in == 64, 5, fraction, "shr")
			x.emit(append(gpRR(false, 7, a, false, 0x81), byte(exponent), byte(exponent>>8), 0, 0), "cmp %s, 0x%x", a32, exponent)
			x.branch(0x75, "jne", other)
		}
		bits()
		shift(true, 4, mantissa, "shl")
		x.emit(append(gpRR(true, 5, a, false, 0x0f, 0xba), 63), "bts %s, 63", a64)
		if saturate {
			x.branch(0xeb, "jmp", done)
			// Positive and beyond 2^64, infinity included, gives MAX.
			x.bind(other)
			nan(zero)
			x.emit(append(gpRR(false, 7, a, false, 0x81), byte(threshold), byte(threshold>>8), 0, 0), "cmp %s, 0x%x", a32, threshold)
			x.emit(gpRR(true, a, a, false, 0x19), "sbb %s, %s", a64, a64)
			x.branch(0xeb, "jmp", done)
		}
	}
	if saturate {
		x.bind(zero)
		xorSelf()
	}
	x.bind(done)
	return x.resolve()
}

func floatConvertPlan(i machine.Instruction) []expansionStep {
	if i.Op == machine.OpIntToFloat {
		return intToFloatPlan(i)
	}
	return floatToIntPlan(i)
}

func (e *ByteAssembler) FloatConvert(ins machine.Instruction) {
	if !e.require(validFloatConvert(ins)) {
		return
	}
	e.expansion(floatConvertPlan(ins))
}
func (e *TextAssembler) FloatConvert(ins machine.Instruction) {
	if !e.valid(func(enc *ByteAssembler) { enc.FloatConvert(ins) }) {
		return
	}
	e.expansion(floatConvertPlan(ins))
}
