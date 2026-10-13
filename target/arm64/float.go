package arm64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func fpMoveToVector(width uint8, vector, integer uint32) uint32 {
	base := uint32(0x1e270000) // FMOV Sd, Wn
	if width == 64 {
		base = 0x9e670000 // FMOV Dd, Xn
	}
	return base | integer<<5 | vector
}

func fpMoveToInteger(width uint8, integer, vector uint32) uint32 {
	base := uint32(0x1e260000) // FMOV Wd, Sn
	if width == 64 {
		base = 0x9e660000 // FMOV Xd, Dn
	}
	return base | vector<<5 | integer
}

func fpArithmeticWord(operation ir.Opcode, width uint8) uint32 {
	base := map[ir.Opcode]uint32{
		ir.OpFAdd: 0x1e202800, ir.OpFSub: 0x1e203800,
		ir.OpFMul: 0x1e200800, ir.OpFDiv: 0x1e201800,
	}[operation]
	if base == 0 {
		panic("arm64: invalid floating operation")
	}
	if width == 64 {
		base |= 0x00400000
	}
	return base
}

func floatPlan(ins machine.Instruction) []vectorConcrete {
	var out []vectorConcrete
	emit := func(w uint32, format string, args ...any) {
		out = append(out, vectorConcrete{w, fmt.Sprintf(format, args...)})
	}
	fpr := func(w uint8) string {
		if w == 64 {
			return "D"
		}
		return "S"
	}
	gpr := func(w uint8) string {
		if w == 64 {
			return "X"
		}
		return "W"
	}
	input := func(r machine.Register, t uint32, w uint8) uint32 {
		if machine.IsVectorRegister(r) {
			return uint32(machine.VectorNumber(r))
		}
		emit(fpMoveToVector(w, t, uint32(r)), "fmov %s%d, %s%d", fpr(w), t, gpr(w), r)
		return t
	}
	w := ins.Bits
	d := uint32(16)
	if machine.IsVectorRegister(ins.A) {
		d = uint32(machine.VectorNumber(ins.A))
	}
	if ins.Op == machine.OpFloatMove {
		if machine.IsVectorRegister(ins.A) && machine.IsVectorRegister(ins.B) {
			s := uint32(machine.VectorNumber(ins.B))
			base := uint32(0x1e204000)
			if w == 64 {
				base |= 0x400000
			}
			if d != s {
				emit(base|s<<5|d, "fmov %s%d, %s%d", fpr(w), d, fpr(w), s)
			}
		} else if machine.IsVectorRegister(ins.A) {
			input(ins.B, d, w)
		} else {
			emit(fpMoveToInteger(w, uint32(ins.A), uint32(machine.VectorNumber(ins.B))), "fmov %s%d, %s%d", gpr(w), ins.A, fpr(w), machine.VectorNumber(ins.B))
		}
		return out
	}
	if ins.Op == machine.OpFloatBits {
		src := input(ins.B, 16, w)
		base, name := uint32(0x1e214000), "fneg"
		if ins.Arithmetic == ir.OpAnd {
			base, name = 0x1e20c000, "fabs"
		}
		if w == 64 {
			base |= 0x400000
		}
		emit(base|src<<5|d, "%s %s%d, %s%d", name, fpr(w), d, fpr(w), src)
	} else if ins.Op == machine.OpFloatUnary {
		sw, word, name := w, uint32(0x1e21c000), "fsqrt"
		if w == 64 {
			word |= 0x400000
		}
		if ins.Arithmetic == ir.OpFConvert {
			sw = 96 - w
			name = "fcvt"
			word = 0x1e22c000
			if w == 32 {
				word = 0x1e624000
			}
		}
		s := input(ins.B, 16, sw)
		emit(word|s<<5|d, "%s %s%d, %s%d", name, fpr(w), d, fpr(sw), s)
	} else {
		a, b := input(ins.B, 16, w), input(ins.C, 17, w)
		if ins.Op == machine.OpFloatFMA {
			c := input(ins.D, 18, w)
			base := uint32(0x1f000000)
			if w == 64 {
				base |= 0x400000
			}
			emit(base|b<<16|c<<10|a<<5|d, "fmadd %s%d, %s%d, %s%d, %s%d", fpr(w), d, fpr(w), a, fpr(w), b, fpr(w), c)
		} else {
			emit(fpArithmeticWord(ins.Arithmetic, w)|b<<16|a<<5|d, "%s %s%d, %s%d, %s%d", map[ir.Opcode]string{ir.OpFAdd: "fadd", ir.OpFSub: "fsub", ir.OpFMul: "fmul", ir.OpFDiv: "fdiv"}[ins.Arithmetic], fpr(w), d, fpr(w), a, fpr(w), b)
		}
	}
	if !machine.IsVectorRegister(ins.A) {
		emit(fpMoveToInteger(w, uint32(ins.A), d), "fmov %s%d, %s%d", gpr(w), ins.A, fpr(w), d)
	}
	return out
}
func FloatSize(ins machine.Instruction) int { return len(floatPlan(ins)) * 4 }
func (e *ByteAssembler) Float(ins machine.Instruction) {
	for _, i := range floatPlan(ins) {
		e.words = append(e.words, i.word)
	}
}
func (e *TextAssembler) Float(ins machine.Instruction) {
	for _, i := range floatPlan(ins) {
		e.line("%s", i.text)
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
