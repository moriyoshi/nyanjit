package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// slotAddress expands OpSlotAddress: A gets the address of spill slot Slot,
// as OpSpill addresses it (SpillOffset+8*Slot(r1), or 8*Slot(r29) without a
// platform stack), with addi, or addis then addi for an offset beyond 16
// bits. It writes only A.
func (p *expansion) slotAddress(ins machine.Instruction) error {
	if ins.Slot < 0 || ins.Slot > (1<<31-1-0x8000-p.emitter.SpillOffset())/8 {
		return machine.ErrUnsupportedOperation
	}
	base, offset := backend.Register(29), ins.Slot*8
	if p.emitter.stack == abi.StackPlatform {
		base, offset = 1, p.emitter.SpillOffset()+offset
	}
	// RA=0 would read as literal zero in the second addi.
	if ins.A == 0 || ins.A == 1 || ins.A == base || ins.A > 31 {
		return machine.ErrUnsupportedOperation
	}
	if offset <= 32767 {
		p.emit(Instruction{Op: Addi, A: ins.A, B: base, Imm: int64(offset)})
		return nil
	}
	low := int64(int16(offset))
	p.emit(Instruction{Op: Addis, A: ins.A, B: base, Imm: (int64(offset) - low) >> 16})
	if low != 0 {
		p.emit(Instruction{Op: Addi, A: ins.A, B: ins.A, Imm: low})
	}
	return nil
}
