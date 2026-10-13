package ppc64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func (p *expansion) float32Result(dst, fp machine.Register) {
	// ISA 2.07 defines XSCVDPSPN's binary32 bits in word 0. MFVSRWZ
	// reads word 1, so POWER8 needs a word rotation before extraction.
	p.emit(Instruction{Op: Xscvdpspn, A: 0, B: fp})
	if p.emitter.cpu < abi.PPC64Power9 {
		p.emit(Instruction{Op: Xxsldwi, A: 0, B: 0, C: 0, Shift: 3})
	}
	p.emit(Instruction{Op: Mfvsrwz, A: dst, B: 0})
}

// F0/F1/F2 are volatile under both C ELF variants. Their VSR0/VSR1/VSR2
// aliases permit POWER8 register transfers without a stack or workspace.
func (p *expansion) floatInput(width uint8, fp, source machine.Register) {
	if machine.IsVectorRegister(source) {
		n := machine.Register(machine.VectorNumber(source))
		if fp != n {
			p.emit(Instruction{Op: Fmr, A: fp, B: n})
		}
		return
	}
	p.emit(Instruction{Op: Mtvsrd, A: fp, B: source})
	if width == 32 {
		// The source IEEE binary32 bits are in word 1 after MTVSRD.
		// Shift them into word 0, which XSCVSPDPN reads. Both operations
		// stay in volatile VSRs and require no general-register temporary.
		// The N form preserves signaling NaNs until arithmetic consumes them.
		p.emit(Instruction{Op: Xxsldwi, A: fp, B: fp, C: fp, Shift: 1})
		p.emit(Instruction{Op: Xscvspdpn, A: fp, B: fp})
	}
}

func (p *expansion) floatResult(width uint8, dst machine.Register) {
	if machine.IsVectorRegister(dst) {
		p.emit(Instruction{Op: Fmr, A: machine.Register(machine.VectorNumber(dst)), B: 0})
		return
	}
	if width == 32 {
		// Single-precision arithmetic already rounded once; repacking through
		// the non-signaling conversion does not add another rounding step.
		p.float32Result(dst, 0)
	} else {
		p.emit(Instruction{Op: Mfvsrd, A: dst, B: 0})
	}
}

func (p *expansion) floatOperation(ins machine.Instruction) error {
	if ins.Bits != 32 && ins.Bits != 64 {
		return machine.ErrUnsupportedOperation
	}
	w := ins.Bits
	input := func(src, temp machine.Register, width uint8) machine.Register {
		if machine.IsVectorRegister(src) {
			return machine.Register(machine.VectorNumber(src))
		}
		p.floatInput(width, temp, src)
		return temp
	}
	result := func(fp machine.Register) {
		if machine.IsVectorRegister(ins.A) {
			return
		}
		if w == 32 {
			p.float32Result(ins.A, fp)
		} else {
			p.emit(Instruction{Op: Mfvsrd, A: ins.A, B: fp})
		}
	}
	if ins.Op == machine.OpFloatMove {
		if machine.IsVectorRegister(ins.A) {
			d := machine.Register(machine.VectorNumber(ins.A))
			if machine.IsVectorRegister(ins.B) {
				src := machine.Register(machine.VectorNumber(ins.B))
				if d != src {
					p.emit(Instruction{Op: Fmr, A: d, B: src})
				}
			} else {
				p.floatInput(w, d, ins.B)
			}
		} else {
			result(machine.Register(machine.VectorNumber(ins.B)))
		}
		return nil
	}
	dst := machine.Register(0)
	if machine.IsVectorRegister(ins.A) {
		dst = machine.Register(machine.VectorNumber(ins.A))
	}
	if ins.Op == machine.OpFloatBits {
		src := input(ins.B, 0, w)
		op := Fneg
		if ins.Arithmetic == ir.OpAnd {
			op = Fabs
		}
		p.emit(Instruction{Op: op, A: dst, B: src})
		result(dst)
		return nil
	}
	if ins.Op == machine.OpFloatUnary {
		sw := w
		if ins.Arithmetic == ir.OpFConvert {
			sw = 96 - w
		}
		src := input(ins.B, 0, sw)
		if ins.Arithmetic == ir.OpFConvert {
			if w == 32 {
				p.emit(Instruction{Op: Frsp, A: dst, B: src})
			} else if dst != src {
				p.emit(Instruction{Op: Fmr, A: dst, B: src})
			}
		} else {
			p.emit(Instruction{Op: Fsqrt, A: dst, B: src, Bits: w})
		}
		result(dst)
		return nil
	}
	a, b := input(ins.B, 0, w), input(ins.C, 1, w)
	c := machine.Register(2)
	op := Fmadd
	if ins.Op == machine.OpFloatFMA {
		c = input(ins.D, 2, w)
	} else {
		var ok bool
		op, ok = map[ir.Opcode]Opcode{ir.OpFAdd: Fadd, ir.OpFSub: Fsub, ir.OpFMul: Fmul, ir.OpFDiv: Fdiv}[ins.Arithmetic]
		if !ok {
			return machine.ErrUnsupportedOperation
		}
	}
	p.emit(Instruction{Op: op, A: dst, B: a, C: b, D: c, Bits: w})
	result(dst)
	return nil
}
