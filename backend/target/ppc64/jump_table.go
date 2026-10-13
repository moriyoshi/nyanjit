package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// OpJumpTable clamps the index into B and jumps through a table of 32-bit
// offsets that follows the branch:
//
//	li     b, n             ; n = len(Table)-1 (lis/ori for larger counts)
//	cmpld  a, b
//	isel   b, a, b, lt      ; b = a < n ? a : n, so entry n is the default
//	mflr   r12              ; keep LR, which BCL overwrites
//	bcl    20, 31, $+4
//	mflr   r11
//	mtlr   r12
//	addi   r11, r11, 40     ; the table
//	sldi   b, b, 2
//	add    b, r11, b
//	lwz    b, 0(b)
//	extsw  b, b
//	add    r11, r11, b
//	mtctr  r11
//	bctr
//	table: .long target-table, ...
//
// The clamp is unconditional, so even a mispredicted bounds check loads an
// entry of the table. r11 and r12 are the expansion temporaries; CR0 and CTR
// are written, and LR is restored.

// tableWord is an expansion's data word: Imm holds its value. Only a jump
// table's expansion has one.
const tableWord Opcode = 255

// validJumpTableOperands checks the registers and the table length of an
// OpJumpTable, whose entries may still be labels: r0 reads as zero in ISEL
// and as a base, and r1, r2, r11, r12 and r13 have fixed roles.
func validJumpTableOperands(ins *machine.Instruction) bool {
	valid := func(r backend.Register) bool { return r > 2 && r < 32 && r != 11 && r != 12 && r != 13 }
	return len(ins.Table) >= 2 && uint64(len(ins.Table)-1) < 1<<31 && valid(ins.A) && valid(ins.B) && ins.A != ins.B
}

// jumpTable expands an OpJumpTable at pc.
func (p *expansion) jumpTable(ins machine.Instruction, pc int) error {
	if !validJumpTableOperands(&ins) {
		return machine.ErrUnsupportedOperation
	}
	a, b := ins.A, ins.B
	p.constant(b, uint64(len(ins.Table)-1), false)
	p.emit(Instruction{Op: Cmpl, B: a, C: b, Bits: 64})
	p.emit(Instruction{Op: Isel, A: b, B: a, C: b, BI: 0})
	p.emit(Instruction{Op: Mflr, A: 12})
	p.emit(Instruction{Op: Bc, BO: 20, BI: 31, Imm: 4, Link: true})
	p.emit(Instruction{Op: Mflr, A: 11})
	p.emit(Instruction{Op: Mtlr, A: 12})
	p.emit(Instruction{Op: Addi, A: 11, B: 11, Imm: 40})
	p.emit(Instruction{Op: Rldicr, A: b, B: b, Shift: 2, Mask: 61})
	p.emit(Instruction{Op: Add, A: b, B: 11, C: b})
	p.emit(Instruction{Op: Lwz, A: b, B: b})
	p.emit(Instruction{Op: Extsw, A: b, B: b})
	p.emit(Instruction{Op: Add, A: 11, B: 11, C: b})
	p.emit(Instruction{Op: Mtctr, A: 11})
	p.emit(Instruction{Op: Bctr})
	start := pc + 4*len(p.instructions)
	for _, target := range ins.Table {
		// Sizing sees labels at PC 0; the offsets only fit once they are PCs.
		if offset := int64(target - start); offset == int64(int32(offset)) {
			p.emit(Instruction{Op: tableWord, Imm: offset})
		} else {
			return machine.ErrInvalidInstruction
		}
	}
	return nil
}
