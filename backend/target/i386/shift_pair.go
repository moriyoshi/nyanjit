package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Paired immediate shifts copy B:HighB to A:HighA. C is a distinct
// scheduler-reserved scratch register for rotations; no hidden GPR is used.
func pairedShiftPlan(i machine.Instruction, p *planCode) {
	lo, hi, tmp, n := i.A, i.HighA, i.C, byte(i.Imm)
	mov := func(d, s backend.Register) {
		if d != s {
			p.code = append(p.code, 0x89, modRM(3, byte(s), byte(d)))
			if p.text {
				p.line("mov %s, %s", name(d), name(s))
			}
		}
	}
	zero := func(d backend.Register) {
		p.code = append32(append(p.code, 0xb8|byte(d)), 0)
		if p.text {
			p.line("mov %s, 0", name(d))
		}
	}
	shift := func(d backend.Register, operation op.Opcode, n byte) {
		if n != 0 {
			p.code = append(p.code, 0xc1, modRM(3, shiftGroup(operation), byte(d)), n)
			if p.text {
				p.line("%s %s, %d", shiftName(operation), name(d), n)
			}
		}
	}
	double := func(d, s backend.Register, left bool, n byte) {
		opcode, mnemonic := byte(0xac), "shrd"
		if left {
			opcode, mnemonic = 0xa4, "shld"
		}
		p.code = append(p.code, 0x0f, opcode, modRM(3, byte(s), byte(d)), n)
		if p.text {
			p.line("%s %s, %s, %d", mnemonic, name(d), name(s), n)
		}
	}
	// Parallel word copies also support swapped source/destination pairs.
	if lo == i.HighB && hi == i.B {
		mov(tmp, i.B)
		mov(hi, i.HighB)
		mov(lo, tmp)
	} else if lo == i.HighB {
		mov(hi, i.HighB)
		mov(lo, i.B)
	} else {
		mov(lo, i.B)
		mov(hi, i.HighB)
	}
	switch i.Arithmetic {
	case op.OpRotateLeft, op.OpRotateRight:
		n &= 63
		if n >= 32 {
			mov(tmp, lo)
			mov(lo, hi)
			mov(hi, tmp)
			n -= 32
		}
		if n != 0 {
			mov(tmp, lo)
			double(lo, hi, i.Arithmetic == op.OpRotateLeft, n)
			double(hi, tmp, i.Arithmetic == op.OpRotateLeft, n)
		}
	case op.OpShiftLeft:
		if n >= 32 {
			mov(hi, lo)
			shift(hi, op.OpShiftLeft, n-32)
			zero(lo)
		} else if n != 0 {
			double(hi, lo, true, n)
			shift(lo, op.OpShiftLeft, n)
		}
	case op.OpShiftRight, op.OpShiftRightArithmetic:
		if n >= 32 {
			mov(lo, hi)
			shift(lo, i.Arithmetic, n-32)
			if i.Arithmetic == op.OpShiftRightArithmetic {
				shift(hi, i.Arithmetic, 31)
			} else {
				zero(hi)
			}
		} else if n != 0 {
			double(lo, hi, false, n)
			shift(hi, i.Arithmetic, n)
		}
	}
}
func (e *ByteAssembler) ShiftInstruction(i machine.Instruction) {
	if !e.require(validInstructionRegisters(i)) {
		return
	}

	if i.PreserveFlags {
		e.preserved(i)
		return
	}
	if i.Bits != 64 {
		e.Shift(i.Arithmetic, i.Bits, i.A, i.B, uint8(i.Imm))
		return
	}
	p := planCode{code: e.code}
	pairedShiftPlan(i, &p)
	e.code = p.code
}
func (e *TextAssembler) ShiftInstruction(i machine.Instruction) {
	if !e.require(validInstructionRegisters(i)) {
		return
	}

	if i.PreserveFlags {
		e.preserved(i)
		return
	}
	if i.Bits != 64 {
		e.Shift(i.Arithmetic, i.Bits, i.A, i.B, uint8(i.Imm))
		return
	}
	p := planCode{text: true, code: e.code}
	pairedShiftPlan(i, &p)
	e.code = p.code
	e.recordPlan(&p)
}
