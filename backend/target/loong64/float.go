package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// f0-f2 are expansion temporaries. The allocator uses f8-f23, which are
// volatile in both LP64D and Go ABIInternal; f24-f31 remain C callee-saves.
func (e *ByteAssembler) floatRR(op uint32, name string, d, a uint32) {
	e.emit(op|a<<5|d, "%s f%d, f%d", name, d, a)
}
func (e *ByteAssembler) floatInput(r backend.Register, temp uint32, bits uint8) uint32 {
	if backend.IsVectorRegister(r) {
		return uint32(backend.VectorNumber(r))
	}
	op, name := uint32(0x4529<<10), "movgr2fr.w"
	if bits == 64 {
		op, name = 0x452a<<10, "movgr2fr.d"
	}
	e.emit(op|uint32(r)<<5|temp, "%s f%d, r%d", name, temp, r)
	return temp
}
func (e *ByteAssembler) floatOutput(d backend.Register, src uint32, bits uint8) {
	op, name := uint32(0x452d<<10), "movfr2gr.s"
	if bits == 64 {
		op, name = 0x452e<<10, "movfr2gr.d"
	}
	e.emit(op|src<<5|uint32(d), "%s r%d, f%d", name, d, src)
	// MOVFR2GR.S sign extends. Values in the general bank carry zero-extended
	// bit patterns, including negative FP32 values and NaNs.
	if bits == 32 {
		e.Narrow(d, d, 32)
	}
}
func (e *ByteAssembler) Float(i machine.Instruction) {
	if !e.require((validInstructionRegisters(i))) {
		return
	}

	if !e.require(validFloat(i)) {
		return
	}

	w := i.Bits
	d := uint32(0)
	if backend.IsVectorRegister(i.A) {
		d = uint32(backend.VectorNumber(i.A))
	}
	if i.Op == machine.OpFloatMove {
		if backend.IsVectorRegister(i.A) {
			if backend.IsVectorRegister(i.B) {
				s := uint32(backend.VectorNumber(i.B))
				if d != s {
					op, name := uint32(0x4525<<10), "fmov.s"
					if w == 64 {
						op, name = 0x4526<<10, "fmov.d"
					}
					e.floatRR(op, name, d, s)
				}
			} else {
				e.floatInput(i.B, d, w)
			}
		} else {
			e.floatOutput(i.A, uint32(backend.VectorNumber(i.B)), w)
		}
		return
	}
	suffix := "s"
	if w == 64 {
		suffix = "d"
	}
	switch i.Op {
	case machine.OpFloatBits:
		s := e.floatInput(i.B, 0, w)
		operation, name := uint32(0x4505<<10), "fneg."
		if i.Arithmetic == op.OpAnd {
			operation, name = 0x4501<<10, "fabs."
		}
		if w == 64 {
			operation += 1 << 10
		}
		e.floatRR(operation, name+suffix, d, s)
	case machine.OpFloatUnary:
		sw, operation, name := w, uint32(0x4511<<10), "fsqrt."+suffix
		if w == 64 {
			operation += 1 << 10
		}
		if i.Arithmetic == op.OpFConvert {
			sw = 96 - w
			operation, name = 0x4646<<10, "fcvt.s.d"
			if w == 64 {
				operation, name = 0x4649<<10, "fcvt.d.s"
			}
		}
		s := e.floatInput(i.B, 0, sw)
		e.floatRR(operation, name, d, s)
	default:
		a, b := e.floatInput(i.B, 0, w), e.floatInput(i.C, 1, w)
		if i.Op == machine.OpFloatFMA {
			c := e.floatInput(i.D, 2, w)
			op := uint32(0x81 << 20)
			if w == 64 {
				op = 0x82 << 20
			}
			e.emit(op|c<<15|b<<10|a<<5|d, "fmadd.%s f%d, f%d, f%d, f%d", suffix, d, a, b, c)
		} else {
			var operation uint32
			var name string
			switch i.Arithmetic {
			case op.OpFAdd:
				operation, name = 0x201<<15, "fadd."
			case op.OpFSub:
				operation, name = 0x205<<15, "fsub."
			case op.OpFMul:
				operation, name = 0x209<<15, "fmul."
			case op.OpFDiv:
				operation, name = 0x20d<<15, "fdiv."
			default:
				e.require(false)
				return
			}
			if w == 64 {
				operation += 1 << 15
			}
			e.emit(operation|b<<10|a<<5|d, "%s%s f%d, f%d, f%d", name, suffix, d, a, b)
		}
	}
	if !backend.IsVectorRegister(i.A) {
		e.floatOutput(i.A, d, w)
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
func (e *ByteAssembler) FloatMemory(i machine.Instruction) error {
	if !e.require(i.Op == machine.OpFloatSpill || i.Op == machine.OpFloatReload) {
		return e.err
	}
	if !e.require((validInstructionRegisters(i))) {
		return e.err
	}

	a, v := e.spillAddress(i.Slot)
	base, off := e.memoryBase(a, int64(v))
	op, name := uint32(0x2b800000), "fld.d"
	if i.Op == machine.OpFloatSpill {
		op, name = 0x2bc00000, "fst.d"
	}
	if i.Bits == 32 {
		op -= 0x800000
		if i.Op == machine.OpFloatSpill {
			name = "fst.s"
		} else {
			name = "fld.s"
		}
	}
	e.emit(op|(uint32(off)&4095)<<10|uint32(base)<<5|uint32(backend.VectorNumber(i.A)), "%s f%d, %d(r%d)", name, backend.VectorNumber(i.A), off, base)
	return nil
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
