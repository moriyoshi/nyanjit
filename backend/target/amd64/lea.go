package amd64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// leaForm is LEA dst, [base + index + disp]: an addition that leaves flags
// alone. A narrow form computes 32 bits and zero-extends them, as the 32-bit
// ADD it replaces does.
type leaForm struct {
	wide             bool
	indexed          bool
	dst, base, index backend.Register
	disp             uint64
}

// noFlagsForm returns the LEA that computes ins when ins asks for a
// flag-preserving form. Register additions of 32 or 64 bits, immediate
// additions and subtractions (whose narrow widths already use 32-bit
// arithmetic), and 64-bit increments and decrements have one.
func noFlagsForm(ins machine.Instruction) (leaForm, bool) {
	if !validGeneralRegisters(ins.A, ins.B) {
		return leaForm{}, false
	}
	switch ins.Op {
	case machine.OpBinary:
		if !validGeneralRegisters(ins.C) || ins.Arithmetic != op.OpAdd || ins.Bits != 0 && ins.Bits != 32 && ins.Bits != 64 {
			return leaForm{}, false
		}
		base, index := ins.B, ins.C
		// RSP cannot be an index, and an RBP or R13 base needs a
		// displacement byte; addition commutes, so either can swap.
		if index == 4 || base&7 == 5 && index&7 != 5 {
			base, index = index, base
		}
		if index == 4 {
			return leaForm{}, false
		}
		return leaForm{wide: ins.Bits != 32, indexed: true, dst: ins.A, base: base, index: index}, true
	case machine.OpBinaryImmediate:
		if ins.Arithmetic != op.OpAdd && ins.Arithmetic != op.OpSub || !(Policy{}).CanBinaryImmediate(ins.Arithmetic, ins.Bits, ins.Imm) {
			return leaForm{}, false
		}
		disp := ins.Imm
		if ins.Arithmetic == op.OpSub {
			disp = -disp
		}
		if ins.Bits != 64 {
			// Only the low 32 bits of the sum survive.
			return leaForm{dst: ins.A, base: ins.B, disp: uint64(int64(int32(uint32(disp))))}, true
		}
		if int64(disp) < -1<<31 || int64(disp) > 1<<31-1 {
			return leaForm{}, false
		}
		return leaForm{wide: true, dst: ins.A, base: ins.B, disp: disp}, true
	case machine.OpInc:
		return leaForm{wide: true, dst: ins.A, base: ins.B, disp: 1}, true
	case machine.OpDec:
		return leaForm{wide: true, dst: ins.A, base: ins.B, disp: ^uint64(0)}, true
	}
	return leaForm{}, false
}

func (f leaForm) rex() byte {
	rex := byte(0x40)
	if f.wide {
		rex |= 8
	}
	if f.dst&8 != 0 {
		rex |= 4
	}
	if f.indexed && f.index&8 != 0 {
		rex |= 2
	}
	if f.base&8 != 0 {
		rex |= 1
	}
	return rex
}

func (f leaForm) size() int {
	n := 2 // opcode, ModRM
	if f.rex() != 0x40 {
		n++
	}
	if f.indexed || f.base&7 == 4 {
		n++ // SIB
	}
	switch {
	case f.disp == 0 && f.base&7 != 5:
	case int64(f.disp) >= -128 && int64(f.disp) <= 127:
		n++
	default:
		n += 4
	}
	return n
}

func (f leaForm) append(code []byte) []byte {
	if rex := f.rex(); rex != 0x40 {
		code = append(code, rex)
	}
	mode := byte(0x80) // disp32
	if f.disp == 0 && f.base&7 != 5 {
		mode = 0
	} else if int64(f.disp) >= -128 && int64(f.disp) <= 127 {
		mode = 0x40 // disp8
	}
	code = append(code, 0x8D)
	switch {
	case f.indexed:
		code = append(code, mode|byte(f.dst&7)<<3|4, byte(f.index&7)<<3|byte(f.base&7))
	case f.base&7 == 4:
		code = append(code, mode|byte(f.dst&7)<<3|4, 0x20|byte(f.base&7)) // SIB without an index
	default:
		code = append(code, mode|byte(f.dst&7)<<3|byte(f.base&7))
	}
	switch mode {
	case 0x40:
		return append(code, byte(f.disp))
	case 0x80:
		return appendDisp32(code, uint32(f.disp))
	}
	return code
}

func (e *ByteAssembler) noFlags(ins machine.Instruction) {
	f, ok := noFlagsForm(ins)
	if !e.require(ok) {
		return
	}
	e.code = f.append(e.code)
}

// BinaryNoFlags adds without writing flags, using LEA. A compiler selects
// it through Instruction.PreserveFlags once flag liveness proves the flags
// of the addition dead.
func (e *ByteAssembler) BinaryNoFlags(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	e.noFlags(machine.Instruction{Op: machine.OpBinary, Arithmetic: operation, Bits: width, A: dst, B: left, C: right})
}

// BinaryImmediateNoFlags adds or subtracts an immediate without writing
// flags, using LEA.
func (e *ByteAssembler) BinaryImmediateNoFlags(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	e.noFlags(machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: operation, Bits: width, A: dst, B: left, Imm: value})
}

// IncrementNoFlags adds one without writing flags, using LEA.
func (e *ByteAssembler) IncrementNoFlags(dst, src backend.Register) {
	e.noFlags(machine.Instruction{Op: machine.OpInc, A: dst, B: src})
}

// DecrementNoFlags subtracts one without writing flags, using LEA.
func (e *ByteAssembler) DecrementNoFlags(dst, src backend.Register) {
	e.noFlags(machine.Instruction{Op: machine.OpDec, A: dst, B: src})
}

func (e *TextAssembler) noFlags(ins machine.Instruction) {
	if !e.valid(func(enc *ByteAssembler) { enc.noFlags(ins) }) {
		return
	}
	f, _ := noFlagsForm(ins)
	dst := amd64Name(f.dst)
	if !f.wide {
		dst = amd64RegisterNames32[f.dst]
	}
	e.line(f.size(), "lea %s, %s", dst, amd64Address(f.base, f.index, 1, f.disp, f.indexed))
}

func (e *TextAssembler) BinaryNoFlags(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	e.noFlags(machine.Instruction{Op: machine.OpBinary, Arithmetic: operation, Bits: width, A: dst, B: left, C: right})
}

func (e *TextAssembler) BinaryImmediateNoFlags(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	e.noFlags(machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: operation, Bits: width, A: dst, B: left, Imm: value})
}

func (e *TextAssembler) IncrementNoFlags(dst, src backend.Register) {
	e.noFlags(machine.Instruction{Op: machine.OpInc, A: dst, B: src})
}

func (e *TextAssembler) DecrementNoFlags(dst, src backend.Register) {
	e.noFlags(machine.Instruction{Op: machine.OpDec, A: dst, B: src})
}
