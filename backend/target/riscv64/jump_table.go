package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// OpJumpTable clamps the index into B without a branch and jumps through a
// table of 32-bit offsets that follows the jump:
//
//	li    x31, n             ; n = len(Table)-1
//	sltu  b, a, x31
//	sub   x30, a, x31
//	neg   b, b               ; all ones when a < n
//	and   x30, x30, b
//	add   b, x31, x30        ; b = a < n ? a : n, so entry n is the default
//	auipc x30, 0
//	addi  x30, x30, 28       ; the table
//	slli  b, b, 2
//	add   b, x30, b
//	lw    b, 0(b)
//	add   b, x30, b
//	jr    b
//	table: .word target-table, ...
//
// The clamp has no branch, so even a mispredicted bounds check loads an
// entry of the table. x30 and x31 are the expansion temporaries.

// validJumpTableOperands checks the registers and the table length of an
// OpJumpTable, whose entries may still be labels.
func validJumpTableOperands(ins *machine.Instruction) bool {
	return len(ins.Table) >= 2 && uint64(len(ins.Table)-1) < 1<<31 && validGeneralRegisters(ins.A, ins.B) && ins.A != ins.B &&
		ins.A != 0 && ins.B != 0 && ins.A < 30 && ins.B < 30
}

// jumpTableHeadWords is the number of instructions before the table.
func jumpTableHeadWords(ins *machine.Instruction) int {
	return len(ConstantWords(31, uint64(len(ins.Table)-1))) + 12
}

// jumpTableSize is the byte size of a valid OpJumpTable.
func jumpTableSize(ins *machine.Instruction) int {
	return 4 * (jumpTableHeadWords(ins) + len(ins.Table))
}

// JumpTable implements OpJumpTable.
func (e *ByteAssembler) JumpTable(ins machine.Instruction) error {
	if !e.require(validJumpTableOperands(&ins)) {
		return e.err
	}
	start := e.pc + 4*jumpTableHeadWords(&ins)
	for _, target := range ins.Table {
		if !e.require(target%2 == 0 && int64(target-start) == int64(int32(target-start))) {
			return e.err
		}
	}
	a, b := uint32(ins.A), uint32(ins.B)
	e.words = append(e.words, ConstantWords(31, uint64(len(ins.Table)-1))...)
	e.words = append(e.words,
		rWord(0, 31, a, 3, b, 0x33),     // sltu b, a, x31
		rWord(0x20, 31, a, 0, 30, 0x33), // sub x30, a, x31
		rWord(0x20, b, 0, 0, b, 0x33),   // neg b, b
		rWord(0, b, 30, 7, 30, 0x33),    // and x30, x30, b
		rWord(0, 30, 31, 0, b, 0x33),    // add b, x31, x30
		30<<7|0x17,                      // auipc x30, 0
		iWord(28, 30, 0, 30, 0x13),      // addi x30, x30, 28
		iWord(2, b, 1, b, 0x13),         // slli b, b, 2
		rWord(0, b, 30, 0, b, 0x33),     // add b, x30, b
		iWord(0, b, 2, b, 0x03),         // lw b, 0(b)
		rWord(0, b, 30, 0, b, 0x33),     // add b, x30, b
		iWord(0, b, 0, 0, 0x67),         // jr b
	)
	if e.literalData == nil {
		e.literalData = map[int]bool{}
	}
	for _, target := range ins.Table {
		e.literalData[len(e.words)] = true
		e.words = append(e.words, uint32(int32(target-start)))
	}
	return nil
}
