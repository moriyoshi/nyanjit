package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// compareSet sets GP A to 1 when the Bits-wide integers in B and C satisfy
// Condition, else 0. It compares into CR0 (cmpw/cmplw at 32 bits and below,
// cmpd/cmpld at 64), after extsb or extsh of both operands into r11 and r12
// for a signed 8- or 16-bit condition; zero-extended operands compare alike
// at 32 bits otherwise. mfcr and rlwinm then extract the CR0 bit, and xori 1
// negates a condition that holds when the bit is clear. Both inputs are read
// before A is written, so A may alias either.
func (p *expansion) compareSet(ins machine.Instruction) error {
	w := ins.Bits
	if ins.Condition > op.BranchAbove || w != 8 && w != 16 && w != 32 && w != 64 || ins.A > 31 || ins.B > 31 || ins.C > 31 {
		return machine.ErrUnsupportedOperation
	}
	bo, bi, err := branchBits(ins.Condition)
	if err != nil {
		return err
	}
	left, right := ins.B, ins.C
	signed := machine.ComparisonFor(ins.Condition) == machine.CompareSigned
	if signed && w < 32 {
		if err := p.signExtend(11, left, w); err != nil {
			return err
		}
		if err := p.signExtend(12, right, w); err != nil {
			return err
		}
		left, right, w = 11, 12, 64
	}
	compare := Cmpl
	if signed {
		compare = Cmp
	}
	bits := uint8(32)
	if w == 64 {
		bits = 64
	}
	p.emit(Instruction{Op: compare, B: left, C: right, Bits: bits})
	p.emit(Instruction{Op: Mfcr, A: ins.A})
	p.emit(Instruction{Op: Rlwinm, A: ins.A, B: ins.A, Shift: bi + 1, Mask: 31, MaskEnd: 31})
	if bo == 4 {
		p.emit(Instruction{Op: Xori, A: ins.A, B: ins.A, Imm: 1})
	}
	return nil
}
