package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func fpArithmeticFunct7(operation op.Opcode, width uint8) uint32 {
	var base uint32
	switch operation {
	case op.OpFSub:
		base = 0x04
	case op.OpFMul:
		base = 0x08
	case op.OpFDiv:
		base = 0x0c
	}
	if operation < op.OpFAdd || operation > op.OpFDiv {
		return 0
	}
	if width == 64 {
		base++
	}
	return base
}

func floatWords(ins machine.Instruction) []uint32 {
	if !validFloat(ins) {
		return nil
	}

	var out []uint32
	emit := func(w uint32) { out = append(out, w) }
	w := ins.Bits
	to, from := uint32(0x78), uint32(0x70)
	if w == 64 {
		to, from = 0x79, 0x71
	}
	input := func(r backend.Register, t uint32, width uint8) uint32 {
		if backend.IsVectorRegister(r) {
			return uint32(backend.VectorNumber(r))
		}
		op := uint32(0x78)
		if width == 64 {
			op = 0x79
		}
		emit(rWord(op, 0, uint32(r), 0, t, 0x53))
		return t
	}
	d := uint32(0)
	if backend.IsVectorRegister(ins.A) {
		d = uint32(backend.VectorNumber(ins.A))
	}
	if ins.Op == machine.OpFloatMove {
		if backend.IsVectorRegister(ins.A) && backend.IsVectorRegister(ins.B) {
			s := uint32(backend.VectorNumber(ins.B))
			op := uint32(0x10)
			if w == 64 {
				op++
			}
			if d != s {
				emit(rWord(op, s, s, 0, d, 0x53))
			}
		} else if backend.IsVectorRegister(ins.A) {
			emit(rWord(to, 0, uint32(ins.B), 0, d, 0x53))
		} else {
			emit(rWord(from, 0, uint32(backend.VectorNumber(ins.B)), 0, uint32(ins.A), 0x53))
			if w == 32 {
				emit(shiftWord(ins.A, ins.A, 32, false, false))
				emit(shiftWord(ins.A, ins.A, 32, true, false))
			}
		}
		return out
	}
	if ins.Op == machine.OpFloatBits {
		src := input(ins.B, 0, w)
		operation, mode := uint32(0x10), uint32(1)
		if w == 64 {
			operation++
		}
		if ins.Arithmetic == op.OpAnd {
			mode = 2
		}
		emit(rWord(operation, src, src, mode, d, 0x53))
	} else if ins.Op == machine.OpFloatUnary {
		sw, operation, rs2, rm := w, uint32(0x2c), uint32(0), uint32(7)
		if w == 64 {
			operation++
		}
		if ins.Arithmetic == op.OpFConvert {
			sw = 96 - w
			operation = 0x21
			rm = 0
			if w == 32 {
				operation, rs2, rm = 0x20, 1, 7
			}
		}
		src := input(ins.B, 0, sw)
		emit(rWord(operation, rs2, src, rm, d, 0x53))
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
	if !backend.IsVectorRegister(ins.A) {
		emit(rWord(from, 0, d, 0, uint32(ins.A), 0x53))
		if w == 32 {
			emit(shiftWord(ins.A, ins.A, 32, false, false))
			emit(shiftWord(ins.A, ins.A, 32, true, false))
		}
	}
	return out
}
func FloatSize(ins machine.Instruction) int { return len(floatWords(ins)) * 4 }
func (e *ByteAssembler) Float(ins machine.Instruction) {
	if !e.require((validInstructionRegisters(ins))) {
		return
	}

	if !e.require(validFloat(ins)) {
		return
	}
	e.words = append(e.words, floatWords(ins)...)
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
		return ins.Arithmetic == op.OpAnd || ins.Arithmetic == op.OpXor
	case machine.OpFloatMove, machine.OpFloatFMA:
		return true
	}
	return false
}
