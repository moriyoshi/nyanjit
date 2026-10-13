package riscv64

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func fpArithmeticFunct7(operation ir.Opcode, width uint8) uint32 {
	base := map[ir.Opcode]uint32{
		ir.OpFAdd: 0x00, ir.OpFSub: 0x04,
		ir.OpFMul: 0x08, ir.OpFDiv: 0x0c,
	}[operation]
	if operation < ir.OpFAdd || operation > ir.OpFDiv {
		panic("riscv64: invalid floating operation")
	}
	if width == 64 {
		base++
	}
	return base
}

func floatWords(ins machine.Instruction) []uint32 {
	var out []uint32
	emit := func(w uint32) { out = append(out, w) }
	w := ins.Bits
	to, from := uint32(0x78), uint32(0x70)
	if w == 64 {
		to, from = 0x79, 0x71
	}
	input := func(r machine.Register, t uint32, width uint8) uint32 {
		if machine.IsVectorRegister(r) {
			return uint32(machine.VectorNumber(r))
		}
		op := uint32(0x78)
		if width == 64 {
			op = 0x79
		}
		emit(rWord(op, 0, uint32(r), 0, t, 0x53))
		return t
	}
	d := uint32(0)
	if machine.IsVectorRegister(ins.A) {
		d = uint32(machine.VectorNumber(ins.A))
	}
	if ins.Op == machine.OpFloatMove {
		if machine.IsVectorRegister(ins.A) && machine.IsVectorRegister(ins.B) {
			s := uint32(machine.VectorNumber(ins.B))
			op := uint32(0x10)
			if w == 64 {
				op++
			}
			if d != s {
				emit(rWord(op, s, s, 0, d, 0x53))
			}
		} else if machine.IsVectorRegister(ins.A) {
			emit(rWord(to, 0, uint32(ins.B), 0, d, 0x53))
		} else {
			emit(rWord(from, 0, uint32(machine.VectorNumber(ins.B)), 0, uint32(ins.A), 0x53))
			if w == 32 {
				emit(shiftWord(ins.A, ins.A, 32, false, false))
				emit(shiftWord(ins.A, ins.A, 32, true, false))
			}
		}
		return out
	}
	if ins.Op == machine.OpFloatBits {
		src := input(ins.B, 0, w)
		op, mode := uint32(0x10), uint32(1)
		if w == 64 {
			op++
		}
		if ins.Arithmetic == ir.OpAnd {
			mode = 2
		}
		emit(rWord(op, src, src, mode, d, 0x53))
	} else if ins.Op == machine.OpFloatUnary {
		sw, op, rs2, rm := w, uint32(0x2c), uint32(0), uint32(7)
		if w == 64 {
			op++
		}
		if ins.Arithmetic == ir.OpFConvert {
			sw = 96 - w
			op = 0x21
			rm = 0
			if w == 32 {
				op, rs2, rm = 0x20, 1, 7
			}
		}
		src := input(ins.B, 0, sw)
		emit(rWord(op, rs2, src, rm, d, 0x53))
	} else {
		a, b := input(ins.B, 0, w), input(ins.C, 1, w)
		if ins.Op == machine.OpFloatFMA {
			c := input(ins.D, 2, w)
			format := uint32(0)
			if w == 64 {
				format = 1
			}
			emit(c<<27 | format<<25 | b<<20 | a<<15 | 7<<12 | d<<7 | 0x43)
		} else {
			emit(rWord(fpArithmeticFunct7(ins.Arithmetic, w), b, a, 7, d, 0x53))
		}
	}
	if !machine.IsVectorRegister(ins.A) {
		emit(rWord(from, 0, d, 0, uint32(ins.A), 0x53))
		if w == 32 {
			emit(shiftWord(ins.A, ins.A, 32, false, false))
			emit(shiftWord(ins.A, ins.A, 32, true, false))
		}
	}
	return out
}
func FloatSize(ins machine.Instruction) int            { return len(floatWords(ins)) * 4 }
func (e *ByteAssembler) Float(ins machine.Instruction) { e.words = append(e.words, floatWords(ins)...) }

func (e *ByteAssembler) FloatUnary(op ir.Opcode, w uint8, d, a machine.Register) {
	e.Float(machine.Instruction{Op: machine.OpFloatUnary, Arithmetic: op, Bits: w, A: d, B: a})
}

func (e *ByteAssembler) FloatBinary(op ir.Opcode, w uint8, d, a, b machine.Register) {
	e.Float(machine.Instruction{Op: machine.OpFloatBinary, Arithmetic: op, Bits: w, A: d, B: a, C: b})
}

func (e *ByteAssembler) FloatFMA(w uint8, d, a, b, c machine.Register) {
	e.Float(machine.Instruction{Op: machine.OpFloatFMA, Bits: w, A: d, B: a, C: b, D: c})
}
