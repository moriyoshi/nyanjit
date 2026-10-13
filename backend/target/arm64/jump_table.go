package arm64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

// OpJumpTable clamps the index into B and jumps through a table of 32-bit
// offsets that follows the branch:
//
//	movz Xb, #n           ; n = len(Table)-1, plus MOVK for wider counts
//	cmp  Xa, Xb
//	csel Xb, Xa, Xb, lo   ; b = a < n ? a : n, so entry n is the default
//	adr  X16, table
//	ldrsw X17, [X16, Xb, lsl #2]
//	add  X16, X16, X17
//	br   X16
//	table: .word target-table, ...
//
// The clamp is unconditional, so even a mispredicted bounds check loads an
// entry of the table. X16 and X17 are the expansion temporaries. NZCV is
// written. The JIT mapping has no PROT_BTI; were it added, the targets would
// need BTI J.

// jumpTableHeadWords is the code before the table: the count's MOVZ/MOVK
// words and six instructions.
func jumpTableHeadWords(n uint64) int {
	_, words := immediateWords(0, n)
	return words + 6
}

// validJumpTableOperands checks the registers and the table length of an
// OpJumpTable, whose entries may still be labels.
func validJumpTableOperands(ins *machine.Instruction) bool {
	return len(ins.Table) >= 2 && validGeneralRegisters(ins.A, ins.B) && ins.A != 31 && ins.B != 31 && ins.A != ins.B &&
		ins.A != 16 && ins.A != 17 && ins.B != 16 && ins.B != 17
}

// validJumpTable checks an OpJumpTable at pc, whose entries are target PCs.
func validJumpTable(ins *machine.Instruction, pc int) bool {
	if !validJumpTableOperands(ins) {
		return false
	}
	table := pc + 4*jumpTableHeadWords(uint64(len(ins.Table)-1))
	for _, target := range ins.Table {
		if target < 0 || target%4 != 0 || int64(target-table) != int64(int32(target-table)) {
			return false
		}
	}
	return true
}

// jumpTableSize is the byte size of a valid OpJumpTable.
func jumpTableSize(ins *machine.Instruction) int {
	return 4 * (jumpTableHeadWords(uint64(len(ins.Table)-1)) + len(ins.Table))
}

// JumpTable implements OpJumpTable.
func (e *ByteAssembler) JumpTable(ins machine.Instruction) error {
	if !e.require(validJumpTable(&ins, e.pc)) {
		return e.err
	}
	a, b := uint32(ins.A), uint32(ins.B)
	count, words := immediateWords(b, uint64(len(ins.Table)-1))
	e.words = append(e.words, count[:words]...)
	e.words = append(e.words,
		0xEB00001F|b<<16|a<<5,         // cmp Xa, Xb
		0x9A800000|b<<16|3<<12|a<<5|b, // csel Xb, Xa, Xb, lo
		0x10000000|16>>2<<5|16,        // adr X16, .+16: the table
		0xB8A07800|b<<16|16<<5|17,     // ldrsw X17, [X16, Xb, lsl #2]
		0x8B110210,                    // add X16, X16, X17
		0xD61F0200,                    // br X16
	)
	start := e.pc + 4*jumpTableHeadWords(uint64(len(ins.Table)-1))
	for _, target := range ins.Table {
		e.words = append(e.words, uint32(int32(target-start)))
	}
	return nil
}

// JumpTable renders the sequence ByteAssembler.JumpTable encodes.
func (e *TextAssembler) JumpTable(ins machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.JumpTable(ins) }); err != nil {
		return err
	}
	e.Constant(ins.B, uint64(len(ins.Table)-1))
	start := e.pc + 4*6
	e.line("cmp X%d, X%d", ins.A, ins.B)
	e.line("csel X%d, X%d, X%d, lo", ins.B, ins.A, ins.B)
	e.line("adr X16, 0x%x", start)
	e.line("ldrsw X17, [X16, X%d, lsl #2]", ins.B)
	e.line("add X16, X16, X17")
	e.line("br X16")
	for _, target := range ins.Table {
		e.lines = append(e.lines, fmt.Sprintf("%04x  .word 0x%x-0x%x", e.pc, target, start))
		e.pc += 4
	}
	return nil
}
