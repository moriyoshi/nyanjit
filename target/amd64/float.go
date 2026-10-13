package amd64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
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
func floatPlan(ins machine.Instruction) []vectorConcrete {
	var out []vectorConcrete
	emit := func(code []byte, format string, args ...any) {
		out = append(out, vectorConcrete{code, fmt.Sprintf(format, args...)})
	}
	transfer := func(width uint8, to bool, fp uint8, gp machine.Register) {
		names := amd64RegisterNames32
		op := "movd"
		if width == 64 {
			names = amd64RegisterNames
			op = "movq"
		}
		text := fmt.Sprintf("%s %s, xmm%d", op, names[gp], fp)
		if to {
			text = fmt.Sprintf("%s xmm%d, %s", op, fp, names[gp])
		}
		emit(appendFPTransfer(nil, width, to, fp, uint8(gp)), "%s", text)
	}
	input := func(reg machine.Register, temp uint8, width uint8) uint8 {
		if machine.IsVectorRegister(reg) {
			return machine.VectorNumber(reg)
		}
		transfer(width, true, temp, reg)
		return temp
	}
	dst := uint8(0)
	if machine.IsVectorRegister(ins.A) {
		dst = machine.VectorNumber(ins.A)
	}
	width := ins.Bits
	if ins.Op == machine.OpFloatMove {
		if machine.IsVectorRegister(ins.A) && machine.IsVectorRegister(ins.B) {
			a, b := machine.VectorNumber(ins.A), machine.VectorNumber(ins.B)
			if a != b {
				emit(vectorRR(0x66, 0x28, a, b, false), "movapd xmm%d, xmm%d", a, b)
			}
		} else if machine.IsVectorRegister(ins.A) {
			transfer(width, true, machine.VectorNumber(ins.A), ins.B)
		} else {
			transfer(width, false, machine.VectorNumber(ins.B), ins.A)
		}
		return out
	}
	prefix, suffix := byte(0xf3), "ss"
	if width == 64 {
		prefix, suffix = 0xf2, "sd"
	}
	if ins.Op == machine.OpFloatBits {
		src := input(ins.B, 1, width)
		if dst != src {
			emit(vectorRR(0x66, 0x28, dst, src, false), "movapd xmm%d, xmm%d", dst, src)
		}
		op, name := byte(0x57), "xorpd"
		if ins.Arithmetic == ir.OpAnd {
			op, name = 0x54, "andpd"
		}
		emit(vectorRR(0x66, op, dst, 0, false), "%s xmm%d, xmm0", name, dst)
	} else if ins.Op == machine.OpFloatUnary {
		sw, opcode, name := width, byte(0x51), "sqrt"+suffix
		if ins.Arithmetic == ir.OpFConvert {
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
		emit(vectorRR(prefix, opcode, dst, src, false), "%s xmm%d, xmm%d", name, dst, src)
	} else if ins.Op == machine.OpFloatBinary {
		left, right := input(ins.B, 0, width), input(ins.C, 1, width)
		if dst != left {
			emit(vectorRR(0x66, 0x28, dst, left, false), "movapd xmm%d, xmm%d", dst, left)
		}
		opcode := map[ir.Opcode]byte{ir.OpFAdd: 0x58, ir.OpFSub: 0x5c, ir.OpFMul: 0x59, ir.OpFDiv: 0x5e}[ins.Arithmetic]
		name := map[ir.Opcode]string{ir.OpFAdd: "add", ir.OpFSub: "sub", ir.OpFMul: "mul", ir.OpFDiv: "div"}[ins.Arithmetic]
		emit(vectorRR(prefix, opcode, dst, right, false), "%s%s xmm%d, xmm%d", name, suffix, dst, right)
	} else if ins.Op == machine.OpFloatFMA {
		z, x, y := input(ins.D, 0, width), input(ins.B, 1, width), input(ins.C, 2, width)
		opcode, v, rm, name := byte(0xb9), x, y, "vfmadd231"+suffix
		if dst == x {
			opcode, v, rm, name = 0xa9, y, z, "vfmadd213"+suffix
		} else if dst == y {
			opcode, v, rm, name = 0xa9, x, z, "vfmadd213"+suffix
		} else if dst != z {
			emit(vectorRR(0x66, 0x28, dst, z, false), "movapd xmm%d, xmm%d", dst, z)
		}
		b2 := byte(0xe2 ^ (dst>>3)<<7 ^ (rm>>3)<<5)
		b3 := byte((^v&15)<<3 | 1)
		if width == 64 {
			b3 |= 0x80
		}
		emit([]byte{0xc4, b2, b3, opcode, 0xc0 | (dst&7)<<3 | rm&7}, "%s xmm%d, xmm%d, xmm%d", name, dst, v, rm)
	}
	if !machine.IsVectorRegister(ins.A) {
		transfer(width, false, dst, ins.A)
	}
	return out
}
func FloatSize(ins machine.Instruction) int {
	n := 0
	for _, i := range floatPlan(ins) {
		n += len(i.code)
	}
	return n
}
func FloatUnarySize(op ir.Opcode, w uint8, d, a machine.Register) int {
	return FloatSize(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}
func FloatBinarySize(w uint8, d, a, b machine.Register) int {
	return FloatSize(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: ir.OpFAdd, Bits: w, A: d, B: a, C: b})
}
func FloatFMASize(w uint8, d, a, b, c machine.Register) int {
	return FloatSize(machine.Instruction{Op: machine.OpFloatFMA, Bits: w, A: d, B: a, C: b, D: c})
}
func (e *ByteAssembler) Float(ins machine.Instruction) {
	for _, i := range floatPlan(ins) {
		e.code = append(e.code, i.code...)
	}
}
func (e *TextAssembler) Float(ins machine.Instruction) {
	for _, i := range floatPlan(ins) {
		e.line(len(i.code), "%s", i.text)
	}
}

func (e *ByteAssembler) FloatUnary(op ir.Opcode, w uint8, d, a machine.Register) {
	e.Float(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}

func (e *ByteAssembler) FloatBinary(op ir.Opcode, w uint8, d, a, b machine.Register) {
	e.Float(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op, Bits: w, A: d, B: a, C: b})
}

func (e *ByteAssembler) FloatFMA(w uint8, d, a, b, c machine.Register) {
	e.Float(machine.Instruction{Op: machine.OpFloatFMA, Bits: w, A: d, B: a, C: b, D: c})
}

func (e *TextAssembler) FloatUnary(op ir.Opcode, w uint8, d, a machine.Register) {
	e.Float(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}

func (e *TextAssembler) FloatBinary(op ir.Opcode, w uint8, d, a, b machine.Register) {
	e.Float(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op, Bits: w, A: d, B: a, C: b})
}

func (e *TextAssembler) FloatFMA(w uint8, d, a, b, c machine.Register) {
	e.Float(machine.Instruction{Op: machine.OpFloatFMA, Bits: w, A: d, B: a, C: b, D: c})
}
