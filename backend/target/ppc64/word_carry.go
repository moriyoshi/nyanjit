package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func (p *expansion) wordCarry(ins machine.Instruction) error {
	if ins.Bits != 0 && ins.Bits != 64 || ins.Arithmetic != op.OpAdd && ins.Arithmetic != op.OpSub || ins.CarryMode > machine.CarryFlags {
		return machine.ErrUnsupportedOperation
	}
	subtract := ins.Arithmetic == op.OpSub
	if ins.Op == machine.OpCarryBit {
		if ins.CarryMode != machine.CarryFlags {
			op := Addc
			if subtract {
				op = Subfc
			}
			p.emit(Instruction{Op: op, A: 12, B: ins.B, C: ins.C})
		}
		p.emit(Instruction{Op: Addi, A: 11})
		op := Addze
		if subtract {
			op = Subfze
		}
		p.emit(Instruction{Op: op, A: ins.A, B: 11})
		if subtract {
			p.emit(Instruction{Op: Neg, A: ins.A, B: ins.A})
		}
		return nil
	}
	switch ins.CarryMode {
	case machine.CarryOperands:
		op := Addc
		if subtract {
			op = Subfc
		}
		p.emit(Instruction{Op: op, A: 11, B: ins.D, C: ins.E})
	case machine.CarryFlags:
	case machine.CarryValue, machine.CarryBitValue:
		source := ins.D
		if ins.CarryMode == machine.CarryValue {
			p.emit(Instruction{Op: Rldicl, A: 11, B: source, Mask: 63})
			source = 11
		}
		op, imm := Addic, int64(-1)
		if subtract {
			op, imm = Subfic, 0
		}
		p.emit(Instruction{Op: op, A: 11, B: source, Imm: imm})
	}
	op := Adde
	if subtract {
		op = Subfe
	}
	p.emit(Instruction{Op: op, A: ins.A, B: ins.B, C: ins.C, Record: true})
	return nil
}
