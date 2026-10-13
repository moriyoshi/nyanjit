package loong64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// OpJumpTable clamps the index into B without a branch and jumps through a
// table of 32-bit offsets that follows the jump:
//
//	li      r20, n           ; n = len(Table)-1
//	sltu    b, a, r20
//	maskeqz r19, a, b
//	masknez b, r20, b
//	or      b, b, r19        ; b = a < n ? a : n, so entry n is the default
//	pcaddi  r19, 5           ; the table
//	alsl.d  b, b, r19, 2
//	ld.w    b, b, 0
//	add.d   b, b, r19
//	jirl    r0, b, 0
//	table: .word target-table, ...
//
// The clamp has no branch, so even a mispredicted bounds check loads an
// entry of the table. r19 and r20 are the expansion temporaries.

// validJumpTableOperands checks the registers and the table length of an
// OpJumpTable, whose entries may still be labels.
func validJumpTableOperands(ins *machine.Instruction) bool {
	return len(ins.Table) >= 2 && uint64(len(ins.Table)-1) < 1<<31 && validGeneralRegisters(ins.A, ins.B) && ins.A != ins.B &&
		ins.A != 0 && ins.B != 0 && ins.A != 19 && ins.A != 20 && ins.B != 19 && ins.B != 20
}

// JumpTable implements OpJumpTable.
func (e *ByteAssembler) JumpTable(ins machine.Instruction) error {
	if !e.require(validJumpTableOperands(&ins)) {
		return e.err
	}
	a, b := ins.A, ins.B
	first := len(e.plan)
	e.Constant(20, uint64(len(ins.Table)-1))
	e.rrr(0x00128000, "sltu", b, a, 20)
	e.rrr(0x00130000, "maskeqz", 19, a, b)
	e.rrr(0x00138000, "masknez", b, 20, b)
	e.rrr(0x00150000, "or", b, b, 19)
	e.emit(0x18000000|5<<5|19, "pcaddi r19, 5")
	e.emit(0x002c0000|1<<15|19<<10|uint32(b)<<5|uint32(b), "alsl.d r%d, r%d, r19, 2", b, b)
	e.emit(0x28800000|uint32(b)<<5|uint32(b), "ld.w r%d, r%d, 0", b, b)
	e.rrr(0x00108000, "add.d", b, b, 19)
	e.emit(0x4c000000|uint32(b)<<5, "jirl r0, r%d, 0", b)
	start := e.pc + 4*(len(e.plan)-first)
	for _, target := range ins.Table {
		// Sizing sees labels; only assembly checks the target PCs.
		if !e.sizeOnly && !e.require(target%4 == 0 && int64(target-start) == int64(int32(target-start))) {
			return e.err
		}
		e.emit(uint32(int32(target-start)), ".word 0x%x-0x%x", target, start)
	}
	return e.err
}
