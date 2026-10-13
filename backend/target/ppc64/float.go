package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func (p *expansion) float32Result(dst, fp backend.Register) {
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
func (p *expansion) floatInput(width uint8, fp, source backend.Register) {
	if backend.IsVectorRegister(source) {
		n := backend.Register(backend.VectorNumber(source))
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

func (p *expansion) floatResult(width uint8, dst backend.Register) {
	if backend.IsVectorRegister(dst) {
		p.emit(Instruction{Op: Fmr, A: backend.Register(backend.VectorNumber(dst)), B: 0})
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
	input := func(src, temp backend.Register, width uint8) backend.Register {
		if backend.IsVectorRegister(src) {
			return backend.Register(backend.VectorNumber(src))
		}
		p.floatInput(width, temp, src)
		return temp
	}
	result := func(fp backend.Register) {
		if backend.IsVectorRegister(ins.A) {
			return
		}
		if w == 32 {
			p.float32Result(ins.A, fp)
		} else {
			p.emit(Instruction{Op: Mfvsrd, A: ins.A, B: fp})
		}
	}
	if ins.Op == machine.OpFloatMove {
		if backend.IsVectorRegister(ins.A) {
			d := backend.Register(backend.VectorNumber(ins.A))
			if backend.IsVectorRegister(ins.B) {
				src := backend.Register(backend.VectorNumber(ins.B))
				if d != src {
					p.emit(Instruction{Op: Fmr, A: d, B: src})
				}
			} else {
				p.floatInput(w, d, ins.B)
			}
		} else {
			result(backend.Register(backend.VectorNumber(ins.B)))
		}
		return nil
	}
	dst := backend.Register(0)
	if backend.IsVectorRegister(ins.A) {
		dst = backend.Register(backend.VectorNumber(ins.A))
	}
	if ins.Op == machine.OpFloatBits {
		// The sign mask selects fabs (OpAnd) or fneg (OpXor).
		operation, ok := floatBitsOpcode(ins.Arithmetic)
		if !ok {
			return machine.ErrUnsupportedOperation
		}
		src := input(ins.B, 0, w)
		p.emit(Instruction{Op: operation, A: dst, B: src})
		result(dst)
		return nil
	}
	if ins.Op == machine.OpFloatUnary {
		if ins.Arithmetic != op.OpFSqrt && ins.Arithmetic != op.OpFConvert {
			return machine.ErrUnsupportedOperation
		}
		sw := w
		if ins.Arithmetic == op.OpFConvert {
			sw = 96 - w
		}
		src := input(ins.B, 0, sw)
		if ins.Arithmetic == op.OpFConvert {
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
	c := backend.Register(2)
	operation := Fmadd
	if ins.Op == machine.OpFloatFMA {
		c = input(ins.D, 2, w)
	} else {
		var ok bool
		operation, ok = floatArithmeticOpcode(ins.Arithmetic)
		if !ok {
			return machine.ErrUnsupportedOperation
		}
	}
	p.emit(Instruction{Op: operation, A: dst, B: a, C: b, D: c, Bits: w})
	result(dst)
	return nil
}

// floatConditionBits gives, for each op.FloatCondition, the CR0 bits after
// fcmpu (0 LT, 1 GT, 2 EQ, 3 FU) whose OR, complemented when not is set,
// is the condition. fcmpu sets exactly one of the four bits.
var floatConditionBits = [...]struct {
	a, b uint8
	not  bool
}{
	op.FloatEqual:                 {2, 2, false},
	op.FloatNotEqual:              {2, 2, true},
	op.FloatLess:                  {0, 0, false},
	op.FloatUnorderedGreaterEqual: {0, 0, true},
	op.FloatLessEqual:             {0, 2, false},
	op.FloatUnorderedGreater:      {0, 2, true},
	op.FloatGreater:               {1, 1, false},
	op.FloatUnorderedLessEqual:    {1, 1, true},
	op.FloatGreaterEqual:          {1, 2, false},
	op.FloatUnorderedLess:         {1, 2, true},
	op.FloatOrdered:               {3, 3, true},
	op.FloatUnordered:             {3, 3, false},
	op.FloatOrderedNotEqual:       {0, 1, false},
	op.FloatUnorderedEqual:        {0, 1, true},
}

// floatCompare sets GP A to 1 when FPRs B and C satisfy the condition in
// Imm, else 0. FPRs hold binary32 values in double format, so one fcmpu
// serves both widths. A condition that is not a single CR0 bit is first
// combined into bit a with cror or crnor; mfcr then copies CR into the low
// word, and rlwinm rotates the bit to bit 63 and clears the rest.
func (p *expansion) floatCompare(ins machine.Instruction) error {
	if ins.A > 31 || !backend.IsVectorRegister(ins.B) || !backend.IsVectorRegister(ins.C) || ins.Bits != 32 && ins.Bits != 64 ||
		ins.Imm >= uint64(len(floatConditionBits)) {
		return machine.ErrUnsupportedOperation
	}
	bits := floatConditionBits[ins.Imm]
	p.emit(Instruction{Op: Fcmpu, B: backend.Register(backend.VectorNumber(ins.B)), C: backend.Register(backend.VectorNumber(ins.C))})
	if bits.not {
		p.emit(Instruction{Op: Crnor, A: backend.Register(bits.a), B: backend.Register(bits.a), C: backend.Register(bits.b)})
	} else if bits.a != bits.b {
		p.emit(Instruction{Op: Cror, A: backend.Register(bits.a), B: backend.Register(bits.a), C: backend.Register(bits.b)})
	}
	p.emit(Instruction{Op: Mfcr, A: ins.A})
	p.emit(Instruction{Op: Rlwinm, A: ins.A, B: ins.A, Shift: bits.a + 1, Mask: 31, MaskEnd: 31})
	return nil
}

func floatBitsOpcode(operation op.Opcode) (Opcode, bool) {
	switch operation {
	case op.OpAnd:
		return Fabs, true
	case op.OpXor:
		return Fneg, true
	}
	return 0, false
}

func floatArithmeticOpcode(operation op.Opcode) (Opcode, bool) {
	switch operation {
	case op.OpFAdd:
		return Fadd, true
	case op.OpFSub:
		return Fsub, true
	case op.OpFMul:
		return Fmul, true
	case op.OpFDiv:
		return Fdiv, true
	}
	return 0, false
}
