package amd64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func appendFPTransfer(code []byte, width uint8, toFP bool, fp, gp uint8) []byte {
	code = append(code, 0x66)
	rex := byte(0x40 | gp>>3 | (fp>>3)<<2)
	if width == 64 {
		rex |= 8
	}
	if rex != 0x40 {
		code = append(code, rex)
	}
	op := byte(0x7e)
	if toFP {
		op = 0x6e
	}
	return append(code, 0x0f, op, 0xc0|(fp&7)<<3|gp&7)
}

// floatPlan appends the expansion of ins to code, reporting whether ins is
// valid.
func floatPlan(code []byte, ins machine.Instruction, lines *[]vectorLine) ([]byte, bool) {
	if !validFloat(ins) || !validInstructionRegisters(ins) {
		return code, false
	}
	text := lines != nil
	mark := len(code)
	line := func(format string, args ...any) {
		*lines = append(*lines, vectorLine{len(code) - mark, fmt.Sprintf(format, args...)})
		mark = len(code)
	}

	rr := func(prefix, opcode byte, reg, rm uint8, wide bool) {
		code = vectorRR(code, prefix, opcode, reg, rm, wide)
	}
	transfer := func(width uint8, to bool, fp uint8, gp backend.Register) {
		names := amd64RegisterNames32
		op := "movd"
		if width == 64 {
			names = amd64RegisterNames
			op = "movq"
		}
		code = appendFPTransfer(code, width, to, fp, uint8(gp))
		if text && to {
			line("%s xmm%d, %s", op, fp, names[gp])
		} else if text {
			line("%s %s, xmm%d", op, names[gp], fp)
		}
	}
	input := func(reg backend.Register, temp uint8, width uint8) uint8 {
		if backend.IsVectorRegister(reg) {
			return backend.VectorNumber(reg)
		}
		transfer(width, true, temp, reg)
		return temp
	}
	dst := uint8(0)
	if backend.IsVectorRegister(ins.A) {
		dst = backend.VectorNumber(ins.A)
	}
	width := ins.Bits
	if ins.Op == machine.OpFloatMove {
		if backend.IsVectorRegister(ins.A) && backend.IsVectorRegister(ins.B) {
			a, b := backend.VectorNumber(ins.A), backend.VectorNumber(ins.B)
			if a != b {
				rr(0x66, 0x28, a, b, false)
				if text {
					line("movapd xmm%d, xmm%d", a, b)
				}
			}
		} else if backend.IsVectorRegister(ins.A) {
			transfer(width, true, backend.VectorNumber(ins.A), ins.B)
		} else {
			transfer(width, false, backend.VectorNumber(ins.B), ins.A)
		}
		return code, true
	}
	prefix, suffix := byte(0xf3), "ss"
	if width == 64 {
		prefix, suffix = 0xf2, "sd"
	}
	if ins.Op == machine.OpFloatBits {
		src := input(ins.B, 1, width)
		if dst != src {
			rr(0x66, 0x28, dst, src, false)
			if text {
				line("movapd xmm%d, xmm%d", dst, src)
			}
		}
		operation, name := byte(0x57), "xorpd"
		if ins.Arithmetic == op.OpAnd {
			operation, name = 0x54, "andpd"
		}
		rr(0x66, operation, dst, 0, false)
		if text {
			line("%s xmm%d, xmm0", name, dst)
		}
	} else if ins.Op == machine.OpFloatUnary {
		sw, opcode, name := width, byte(0x51), "sqrt"+suffix
		if ins.Arithmetic == op.OpFConvert {
			sw = 96 - width
			opcode = 0x5a
			name = "cvtss2sd"
			prefix = 0xf3
			if width == 32 {
				name = "cvtsd2ss"
				prefix = 0xf2
			}
		}
		src := input(ins.B, 0, sw)
		rr(prefix, opcode, dst, src, false)
		if text {
			line("%s xmm%d, xmm%d", name, dst, src)
		}
	} else if ins.Op == machine.OpFloatBinary {
		left, right := input(ins.B, 0, width), input(ins.C, 1, width)
		if dst != left {
			rr(0x66, 0x28, dst, left, false)
			if text {
				line("movapd xmm%d, xmm%d", dst, left)
			}
		}
		opcode, name := sseArithmetic(ins.Arithmetic)
		rr(prefix, opcode, dst, right, false)
		if text {
			line("%s%s xmm%d, xmm%d", name, suffix, dst, right)
		}
	} else if ins.Op == machine.OpFloatFMA {
		z, x, y := input(ins.D, 0, width), input(ins.B, 1, width), input(ins.C, 2, width)
		opcode, source, rm, name := byte(0xb9), x, y, "vfmadd231"+suffix
		if dst == x {
			opcode, source, rm, name = 0xa9, y, z, "vfmadd213"+suffix
		} else if dst == y {
			opcode, source, rm, name = 0xa9, x, z, "vfmadd213"+suffix
		} else if dst != z {
			rr(0x66, 0x28, dst, z, false)
			if text {
				line("movapd xmm%d, xmm%d", dst, z)
			}
		}
		b2 := byte(0xe2 ^ (dst>>3)<<7 ^ (rm>>3)<<5)
		b3 := byte((^source&15)<<3 | 1)
		if width == 64 {
			b3 |= 0x80
		}
		code = append(code, 0xc4, b2, b3, opcode, 0xc0|(dst&7)<<3|rm&7)
		if text {
			line("%s xmm%d, xmm%d, xmm%d", name, dst, source, rm)
		}
	}
	if !backend.IsVectorRegister(ins.A) {
		transfer(width, false, dst, ins.A)
	}
	return code, true
}
func FloatSize(ins machine.Instruction) int {
	var buf [32]byte
	code, ok := floatPlan(buf[:0], ins, nil)
	if !ok {
		return 0
	}
	return len(code)
}
func FloatUnarySize(op op.Opcode, w uint8, d, a backend.Register) int {
	return FloatSize(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}
func FloatBinarySize(w uint8, d, a, b backend.Register) int {
	return FloatSize(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op.OpFAdd, Bits: w, A: d, B: a, C: b})
}
func FloatFMASize(w uint8, d, a, b, c backend.Register) int {
	return FloatSize(machine.Instruction{Op: machine.OpFloatFMA, Bits: w, A: d, B: a, C: b, D: c})
}
func (e *ByteAssembler) Float(ins machine.Instruction) {
	if !e.require((validInstructionRegisters(ins))) {
		return
	}

	if !e.require(validFloat(ins)) {
		return
	}

	e.code, _ = floatPlan(e.code, ins, nil)
}
func (e *TextAssembler) Float(ins machine.Instruction) {
	if !e.valid(func(enc *ByteAssembler) { enc.Float(ins) }) {
		return
	}
	var lines []vectorLine
	floatPlan(nil, ins, &lines)
	for _, i := range lines {
		e.line(i.size, "%s", i.text)
	}
}

func (e *ByteAssembler) FloatUnary(op op.Opcode, w uint8, d, a backend.Register) {
	if !e.require((validFloatRegisters(d, a))) {
		return
	}

	e.Float(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}

func (e *ByteAssembler) FloatBinary(op op.Opcode, w uint8, d, a, b backend.Register) {
	if !e.require((validFloatRegisters(d, a, b))) {
		return
	}

	e.Float(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op, Bits: w, A: d, B: a, C: b})
}

func (e *ByteAssembler) FloatFMA(w uint8, d, a, b, c backend.Register) {
	if !e.require((validFloatRegisters(d, a, b, c))) {
		return
	}

	e.Float(machine.Instruction{Op: machine.OpFloatFMA, Bits: w, A: d, B: a, C: b, D: c})
}

func (e *TextAssembler) FloatUnary(op op.Opcode, w uint8, d, a backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.FloatUnary(op, w, d, a) }) {
		return
	}
	e.Float(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}

func (e *TextAssembler) FloatBinary(op op.Opcode, w uint8, d, a, b backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.FloatBinary(op, w, d, a, b) }) {
		return
	}
	e.Float(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op, Bits: w, A: d, B: a, C: b})
}

func (e *TextAssembler) FloatFMA(w uint8, d, a, b, c backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.FloatFMA(w, d, a, b, c) }) {
		return
	}
	e.Float(machine.Instruction{Op: machine.OpFloatFMA, Bits: w, A: d, B: a, C: b, D: c})
}

func validFloat(ins machine.Instruction) bool {
	if !validInstructionRegisters(ins) || ins.Bits != 32 && ins.Bits != 64 {
		return false
	}
	switch ins.Op {
	case machine.OpFloatBinary:
		switch ins.Arithmetic {
		case op.OpFAdd, op.OpFSub, op.OpFMul, op.OpFDiv:
			return true
		}
		return false
	case machine.OpFloatUnary:
		return ins.Arithmetic == op.OpFSqrt || ins.Arithmetic == op.OpFConvert
	case machine.OpFloatBits:
		// The result is formed in A against the mask in XMM0.
		return backend.IsVectorRegister(ins.A) && backend.VectorNumber(ins.A) != 0 &&
			(ins.Arithmetic == op.OpAnd || ins.Arithmetic == op.OpXor)
	case machine.OpFloatMove, machine.OpFloatFMA:
		return true
	}
	return false
}

// sseArithmetic is the SSE opcode and mnemonic stem of an FP operation.
func sseArithmetic(operation op.Opcode) (byte, string) {
	switch operation {
	case op.OpFAdd:
		return 0x58, "add"
	case op.OpFSub:
		return 0x5c, "sub"
	case op.OpFMul:
		return 0x59, "mul"
	case op.OpFDiv:
		return 0x5e, "div"
	}
	return 0, ""
}
