package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// leaForm is LEA dst, [base + index + disp]: an addition that leaves flags
// alone.
type leaForm struct {
	indexed          bool
	dst, base, index backend.Register
	disp             int32
}

// leaFor returns the LEA that computes the 32 bits of ins: a register
// addition, an immediate addition or subtraction (whose 8- and 16-bit
// widths are not masked either), an increment or a decrement. The caller
// zero-extends a masked 8- or 16-bit addition.
func leaFor(ins machine.Instruction) (leaForm, bool) {
	switch ins.Op {
	case machine.OpBinary:
		if ins.Arithmetic != op.OpAdd {
			return leaForm{}, false
		}
		base, index := ins.B, ins.C
		// ESP cannot be an index, and an EBP base needs a displacement
		// byte; addition commutes, so either can swap.
		if index == 4 || base == 5 && index != 5 {
			base, index = index, base
		}
		if index == 4 {
			return leaForm{}, false
		}
		return leaForm{indexed: true, dst: ins.A, base: base, index: index}, true
	case machine.OpBinaryImmediate:
		if ins.Arithmetic != op.OpAdd && ins.Arithmetic != op.OpSub {
			return leaForm{}, false
		}
		disp := int32(uint32(ins.Imm))
		if ins.Arithmetic == op.OpSub {
			disp = -disp // wraps as SUB does
		}
		return leaForm{dst: ins.A, base: ins.B, disp: disp}, true
	case machine.OpInc:
		return leaForm{dst: ins.A, base: ins.B, disp: 1}, true
	case machine.OpDec:
		return leaForm{dst: ins.A, base: ins.B, disp: -1}, true
	}
	return leaForm{}, false
}

func (f leaForm) size() int {
	n := 2 // opcode, ModRM
	if f.indexed || f.base == 4 {
		n++ // SIB
	}
	switch {
	case f.disp == 0 && f.base != 5:
	case f.disp >= -128 && f.disp <= 127:
		n++
	default:
		n += 4
	}
	return n
}

func (f leaForm) append(code []byte) []byte {
	if f.indexed {
		return appendIndexed(code, 0x8d, f.dst, f.base, f.index, 1, uint64(uint32(f.disp)))
	}
	return appendBase(code, 0x8d, f.dst, f.base, uint64(uint32(f.disp)))
}

func (f leaForm) text() string {
	if f.indexed {
		return address(f.base, f.index, 1, uint64(uint32(f.disp)))
	}
	return offset(f.base, uint64(uint32(f.disp)))
}

// BinaryNoFlags computes OpBinary without the flags of its AND mask. A
// compiler selects it through Instruction.PreserveFlags once flag liveness
// proves the flags dead. A 32-bit addition is a LEA and writes no flags; an
// 8- or 16-bit result is zero-extended by MOVZX, after a LEA for an
// addition into another register and otherwise after the operation itself,
// whose flags FlagFacts reports.
func (e *ByteAssembler) BinaryNoFlags(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	e.preserved(machine.Instruction{Op: machine.OpBinary, Arithmetic: operation, Bits: width, A: dst, B: left, C: right, PreserveFlags: true})
}

// BinaryImmediateNoFlags adds or subtracts an immediate with LEA, or ANDs
// with 0xff or 0xffff by MOVZX; neither writes flags.
func (e *ByteAssembler) BinaryImmediateNoFlags(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	e.preserved(machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: operation, Bits: width, A: dst, B: left, Imm: value, PreserveFlags: true})
}

// IncrementNoFlags adds one without writing flags, using LEA.
func (e *ByteAssembler) IncrementNoFlags(dst, src backend.Register) {
	e.preserved(machine.Instruction{Op: machine.OpInc, A: dst, B: src, PreserveFlags: true})
}

// DecrementNoFlags subtracts one without writing flags, using LEA.
func (e *ByteAssembler) DecrementNoFlags(dst, src backend.Register) {
	e.preserved(machine.Instruction{Op: machine.OpDec, A: dst, B: src, PreserveFlags: true})
}

func (e *TextAssembler) BinaryNoFlags(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	e.preserved(machine.Instruction{Op: machine.OpBinary, Arithmetic: operation, Bits: width, A: dst, B: left, C: right, PreserveFlags: true})
}

func (e *TextAssembler) BinaryImmediateNoFlags(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	e.preserved(machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: operation, Bits: width, A: dst, B: left, Imm: value, PreserveFlags: true})
}

func (e *TextAssembler) IncrementNoFlags(dst, src backend.Register) {
	e.preserved(machine.Instruction{Op: machine.OpInc, A: dst, B: src, PreserveFlags: true})
}

func (e *TextAssembler) DecrementNoFlags(dst, src backend.Register) {
	e.preserved(machine.Instruction{Op: machine.OpDec, A: dst, B: src, PreserveFlags: true})
}
