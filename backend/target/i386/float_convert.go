package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// validFloatConvert checks an OpIntToFloat or OpFloatToInt. The float
// operand is an XMM register; the integer is a GP register, or for 64 bits
// a pair. A 64-bit source also names two scratch slots, and a 64-bit result
// is fixed in EDX:EAX.
func validFloatConvert(ins machine.Instruction) bool {
	if !validGeneralRegisters(ins.HighA, ins.HighB, ins.HighC) || ins.Bits != 32 && ins.Bits != 64 {
		return false
	}
	xmm := func(r backend.Register) bool { return backend.IsVectorRegister(r) && backend.VectorNumber(r) < 8 }
	if ins.Op == machine.OpIntToFloat {
		if ins.Arithmetic != op.OpFFromSigned && ins.Arithmetic != op.OpFFromUnsigned || !xmm(ins.A) || !validGeneralRegisters(ins.B) {
			return false
		}
		return ins.Imm == 32 || ins.Imm == 64 && ins.Slot >= 0
	}
	if ins.Arithmetic != op.OpFToSigned && ins.Arithmetic != op.OpFToUnsigned || !xmm(ins.B) || !validGeneralRegisters(ins.A) ||
		ins.Disp != 32 && ins.Disp != 64 || !op.ConversionSemantics(ins.Imm).Valid() {
		return false
	}
	return ins.Bits == 32 || ins.A == 0 && ins.HighA == 2
}

func floatConvertPlan(ins machine.Instruction, frame slotFrame, text bool) []expansionStep {
	x := &expansion{text: text, frame: frame}
	if ins.Op == machine.OpIntToFloat {
		intToFloat(x, ins)
	} else if ins.Arithmetic == op.OpFToSigned && ins.Bits == 32 {
		floatToInt32(x, ins)
	} else {
		floatToInt(x, ins)
	}
	return x.resolve()
}

// intToFloat converts B (with HighB) to XMM A. Only A is written among the
// FP registers: values that need more than the hardware conversion are
// assembled in GP registers or the slots and combined with memory operands.
func intToFloat(x *expansion, ins machine.Instruction) {
	a, b, hi := byte(backend.VectorNumber(ins.A)), ins.B, ins.HighB
	signed := ins.Arithmetic == op.OpFFromSigned
	prefix, suffix := byte(0xf3), "ss"
	if ins.Bits == 64 {
		prefix, suffix = 0xf2, "sd"
	}
	narrow := func() {
		if ins.Bits == 32 {
			x.sse(0xf2, 0x5a, a, a, "cvtsd2ss xmm%d, xmm%d", a, a)
		}
	}
	if ins.Imm == 32 {
		if signed {
			x.sse(prefix, 0x2a, a, byte(b), "cvtsi2%s xmm%d, %s", suffix, a, name(b))
			return
		}
		// CVTSI2SD reads a signed word. Of 2^31 and more, B << 21 is the
		// binary64 pattern but for the exponent: its bit 52 is B's top bit.
		// Adding 1053 to the exponent field gives B exactly, and binary32
		// rounds once from there. EAX is the only GP temporary.
		big, end := x.label(), x.label()
		x.alu(aluTest, b, b)
		x.branch(ccSign, big)
		x.sse(prefix, 0x2a, a, byte(b), "cvtsi2%s xmm%d, %s", suffix, a, name(b))
		x.branch(ccAlways, end)
		x.bind(big)
		x.sse(0x66, 0x6e, a, byte(b), "movd xmm%d, %s", a, name(b))
		x.emit([]byte{0x66, 0x0f, 0x73, modRM(3, 6, a), 21}, "psllq xmm%d, 21", a)
		x.emit([]byte{0x66, 0x0f, 0xc5, modRM(3, 0, a), 3}, "pextrw EAX, xmm%d, 3", a)
		x.immediate(groupAdd, 0, 0x41d0)
		x.emit([]byte{0x66, 0x0f, 0xc4, modRM(3, a, 0), 3}, "pinsrw xmm%d, EAX, 3", a)
		narrow()
		x.bind(end)
		return
	}
	// The pair hi:lo is (2^84 + h*2^32) - (2^84 + 2^52) + (2^52 + lo), with
	// h = hi, or hi + 2^31 when signed (the low terms gain 2^63). The first
	// two terms are exact binary64 patterns whose difference is exact, so
	// the final addition is the only rounding.
	s := ins.Slot
	x.store(s, hi)
	if signed {
		x.memoryImmediate(groupXor, s, 0x80000000)
	}
	x.storeImmediate(s+1, 0x45300000)
	x.sseMemory(0xf2, 0x10, a, s, "movsd")
	if signed {
		x.storeImmediate(s, 0x80100000)
	} else {
		x.storeImmediate(s, 0x00100000)
	}
	x.sseMemory(0xf2, 0x5c, a, s, "subsd")
	x.store(s, b)
	if ins.Bits == 32 {
		// Rounding to binary64 first could round twice. From 2^53 on,
		// round to odd at bit 11 instead (fold the low bits into a sticky
		// bit 11): the sum is then exact and binary32 rounds once.
		exact, odd := x.label(), x.label()
		x.immediate(groupCmp, hi, 0x200000)
		if signed {
			x.branch(ccGreaterEqual, odd)
			x.immediate(groupCmp, hi, 0xffe00000)
			x.branch(ccGreaterEqual, exact)
		} else {
			x.branch(ccBelow, exact)
		}
		x.bind(odd)
		x.testImmediate(b, 0x7ff)
		x.branch(ccEqual, exact)
		x.memoryImmediate(groupOr, s, 0x800)
		x.memoryImmediate(groupAnd, s, 0xfffff800)
		x.bind(exact)
	}
	x.storeImmediate(s+1, 0x43300000)
	x.sseMemory(0xf2, 0x58, a, s, "addsd")
	narrow()
}

// floatToInt32 truncates XMM B to a signed word in A with CVTTSD2SI or
// CVTTSS2SI. Saturation fixes up the indefinite result 0x80000000: NaN gives
// 0, and otherwise B's sign selects MIN or MAX.
func floatToInt32(x *expansion, ins machine.Instruction) {
	a, b := ins.A, byte(backend.VectorNumber(ins.B))
	prefix, suffix, packed := byte(0xf3), "ss", byte(0)
	if ins.Disp == 64 {
		prefix, suffix, packed = 0xf2, "sd", 0x66
	}
	x.sse(prefix, 0x2c, byte(a), b, "cvtt%s2si %s, xmm%d", suffix, name(a), b)
	if op.ConversionSemantics(ins.Imm) != op.ConversionSaturate {
		return
	}
	nan, end := x.label(), x.label()
	x.immediate(groupCmp, a, 0x80000000)
	x.branch(ccNotEqual, end)
	x.sse(packed, 0x2e, b, b, "ucomi%s xmm%d, xmm%d", suffix, b, b)
	x.branch(ccParity, nan)
	x.sse(packed, 0x50, byte(a), b, "movmskp%c %s, xmm%d", suffix[1], name(a), b)
	x.immediate(groupAnd, a, 1)
	x.immediate(groupAdd, a, 0x7fffffff)
	x.branch(ccAlways, end)
	x.bind(nan)
	x.alu(aluXor, a, a)
	x.bind(end)
}

// floatToInt truncates XMM B by decoding its bits in EAX, ECX and EDX: the
// significand, aligned to bit 63 of EDX:EAX, shifts right by 63 minus the
// exponent. Out-of-range values saturate and NaN gives 0, for both
// semantics. A 64-bit result stays in EDX:EAX; a word moves to A.
func floatToInt(x *expansion, ins machine.Instruction) {
	const eax, ecx, edx = backend.Register(0), backend.Register(1), backend.Register(2)
	b := byte(backend.VectorNumber(ins.B))
	signed := ins.Arithmetic == op.OpFToSigned
	wide := ins.Disp == 64
	packed, suffix := byte(0), "ss"
	exponent, mask, bias := byte(23), uint32(0xff), uint32(127)
	if wide {
		packed, suffix = 0x66, "sd"
		exponent, mask, bias = 20, 0x7ff, 1023
	}
	limit := uint32(ins.Bits) // the first exponent out of range
	if signed {
		limit--
	}
	zero, saturate, end := x.label(), x.label(), x.label()
	x.sse(packed, 0x2e, b, b, "ucomi%s xmm%d, xmm%d", suffix, b, b)
	x.branch(ccParity, zero)
	if wide {
		// PEXTRW reads the high word without writing an FP register.
		x.sse(0x66, 0x7e, b, byte(eax), "movd EAX, xmm%d", b)
		x.emit([]byte{0x66, 0x0f, 0xc5, modRM(3, byte(edx), b), 3}, "pextrw EDX, xmm%d, 3", b)
		x.shift(shiftLeft, edx, 16)
		x.emit([]byte{0x66, 0x0f, 0xc5, modRM(3, byte(ecx), b), 2}, "pextrw ECX, xmm%d, 2", b)
		x.alu(aluOr, edx, ecx)
	} else {
		x.sse(0x66, 0x7e, b, byte(edx), "movd EDX, xmm%d", b)
	}
	if !signed {
		x.alu(aluTest, edx, edx)
		x.branch(ccSign, zero)
	}
	x.mov(ecx, edx)
	x.shift(shiftRight, ecx, exponent)
	x.immediate(groupAnd, ecx, mask)
	x.immediate(groupSub, ecx, bias)
	x.branch(ccLess, zero) // |B| < 1
	x.immediate(groupCmp, ecx, limit)
	x.branch(ccAboveEqual, saturate)
	if wide {
		x.double(true, edx, eax, 11)
		if ins.Bits == 64 {
			x.shift(shiftLeft, eax, 11)
		}
	} else {
		x.shift(shiftLeft, edx, 8)
	}
	x.immediate(groupOr, edx, 0x80000000) // the implicit bit
	if ins.Bits == 64 {
		if !wide {
			x.alu(aluXor, eax, eax)
		}
		x.immediate(groupXor, ecx, 63)
		x.double(false, eax, edx, -1)
		x.shiftCL(shiftRight, edx)
		small := x.label()
		x.emit([]byte{0xf6, modRM(3, 0, byte(ecx)), 32}, "test CL, 32")
		x.branch(ccEqual, small)
		x.mov(eax, edx)
		x.alu(aluXor, edx, edx)
		x.bind(small)
		if signed {
			x.sse(packed, 0x50, byte(ecx), b, "movmskp%c ECX, xmm%d", suffix[1], b)
			x.emit([]byte{0xf6, modRM(3, 0, byte(ecx)), 1}, "test CL, 1")
			x.branch(ccEqual, end)
			x.negate64(eax, edx)
		}
	} else {
		x.immediate(groupXor, ecx, 31)
		x.shiftCL(shiftRight, edx)
		x.mov(eax, edx)
	}
	x.branch(ccAlways, end)
	x.bind(saturate)
	if signed {
		// MAX plus the sign bit is MIN.
		x.sse(packed, 0x50, byte(ecx), b, "movmskp%c ECX, xmm%d", suffix[1], b)
		x.immediate(groupAnd, ecx, 1)
		x.immediate(groupOr, eax, 0xffffffff)
		x.emit(append32([]byte{0xb8 | byte(edx)}, 0x7fffffff), "mov EDX, 0x7fffffff")
		x.alu(aluAdd, eax, ecx)
		x.immediate(groupAdc, edx, 0)
	} else {
		x.immediate(groupOr, eax, 0xffffffff)
		if ins.Bits == 64 {
			x.mov(edx, eax)
		}
	}
	x.branch(ccAlways, end)
	x.bind(zero)
	x.alu(aluXor, eax, eax)
	if ins.Bits == 64 {
		x.alu(aluXor, edx, edx)
	}
	x.bind(end)
	if ins.Bits == 32 {
		x.mov(ins.A, eax)
	}
}

func (e *ByteAssembler) FloatConvert(ins machine.Instruction) {
	if !e.require(validFloatConvert(ins)) {
		return
	}
	emitExpansion(e, floatConvertPlan(ins, e.slotFrame(), false))
}
func (e *TextAssembler) FloatConvert(ins machine.Instruction) {
	if !e.require(validFloatConvert(ins)) {
		return
	}
	recordExpansion(e, floatConvertPlan(ins, e.slotFrame(), true))
}
