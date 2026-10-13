package amd64

import (
	"encoding/binary"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// OpJumpTable clamps the index into B and jumps through a table of 32-bit
// offsets that follows the branch:
//
//	mov    b32, n                 ; n = len(Table)-1
//	cmp    a, b
//	cmovb  b, a                   ; b = a < n ? a : n, so entry n is the default
//	lea    a, [rip+table]
//	movsxd b, dword [a+b*4]
//	add    b, a
//	notrack jmp b                 ; the 3E prefix keeps a CET-IBT build valid
//	table: dd target-table, ...
//
// The clamp is unconditional, so even a mispredicted bounds check loads an
// entry of the table. A is overwritten; the flags are written.

// validJumpTableOperands checks the registers and the table length of an
// OpJumpTable, whose entries may still be labels. RSP cannot be an index,
// and RBP and R13 need a displacement as a base.
func validJumpTableOperands(ins *machine.Instruction) bool {
	return len(ins.Table) >= 2 && uint64(len(ins.Table)-1) <= 0xffffffff && validGeneralRegisters(ins.A, ins.B) && ins.A != ins.B &&
		ins.A&7 != 5 && ins.B != 4
}

// jumpTableHeadSize is the size of the code before the table.
func jumpTableHeadSize(b backend.Register) int {
	size := 5 + 3 + 4 + 7 + 4 + 3 + 3
	if b >= 8 {
		size += 2 // REX on MOV and JMP
	}
	return size
}

// validJumpTable checks an OpJumpTable at pc, whose entries are target PCs.
func validJumpTable(ins *machine.Instruction, pc int) bool {
	if !validJumpTableOperands(ins) {
		return false
	}
	table := pc + jumpTableHeadSize(ins.B)
	for _, target := range ins.Table {
		if target < 0 || int64(target-table) != int64(int32(target-table)) {
			return false
		}
	}
	return true
}

// jumpTableSize is the byte size of a valid OpJumpTable.
func jumpTableSize(ins *machine.Instruction) int {
	return jumpTableHeadSize(ins.B) + 4*len(ins.Table)
}

// JumpTable implements OpJumpTable.
func (e *ByteAssembler) JumpTable(ins machine.Instruction) error {
	if !e.require(validJumpTable(&ins, e.pc)) {
		return e.err
	}
	a, b := uint8(ins.A), uint8(ins.B)
	start := e.pc + jumpTableHeadSize(ins.B)
	code := appendMovImm64(e.code, b, uint64(len(ins.Table)-1))
	code = appendREX(code, b, a)
	code = append(code, 0x39, 0xC0|(b&7)<<3|(a&7)) // CMP a, b
	code = appendREX(code, b, a)
	code = append(code, 0x0F, 0x42, 0xC0|(b&7)<<3|(a&7)) // CMOVB b, a
	lea := len(code) + 7 - len(e.code) + e.pc
	code = appendREX(code, a, 0)
	code = append(code, 0x8D, (a&7)<<3|5) // LEA a, [RIP+disp32]
	code = binary.LittleEndian.AppendUint32(code, uint32(int32(start-lea)))
	rex := byte(0x48) // W
	if b >= 8 {
		rex |= 0x06 // R and X
	}
	if a >= 8 {
		rex |= 0x01 // B
	}
	code = append(code, rex, 0x63, (b&7)<<3|4, 0x80|(b&7)<<3|(a&7)) // MOVSXD b, [a+b*4]
	code = appendREX(code, a, b)
	code = append(code, 0x01, 0xC0|(a&7)<<3|(b&7)) // ADD b, a
	code = append(code, 0x3E)                      // NOTRACK
	if b >= 8 {
		code = append(code, 0x41)
	}
	code = append(code, 0xFF, 0xE0|(b&7)) // JMP b
	for _, target := range ins.Table {
		code = binary.LittleEndian.AppendUint32(code, uint32(int32(target-start)))
	}
	e.code = code
	return nil
}

// JumpTable renders the sequence ByteAssembler.JumpTable encodes.
func (e *TextAssembler) JumpTable(ins machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.JumpTable(ins) }); err != nil {
		return err
	}
	a, b := amd64Name(ins.A), amd64Name(ins.B)
	start := e.pc + jumpTableHeadSize(ins.B)
	e.Constant(ins.B, uint64(len(ins.Table)-1))
	e.line(3, "cmp %s, %s", a, b)
	e.line(4, "cmovb %s, %s", b, a)
	e.line(7, "lea %s, [rip+0x%x]", a, start)
	e.line(4, "movsxd %s, dword [%s+%s*4]", b, a, b)
	e.line(3, "add %s, %s", b, a)
	size := 3
	if ins.B >= 8 {
		size++
	}
	e.line(size, "notrack jmp %s", b)
	for _, target := range ins.Table {
		e.line(4, ".long 0x%x-0x%x", target, start)
	}
	return nil
}
