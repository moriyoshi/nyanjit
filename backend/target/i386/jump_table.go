package i386

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// OpJumpTable clamps the index into B and jumps through a table of 32-bit
// offsets that follows the branch:
//
//	mov   b, n                ; n = len(Table)-1
//	cmp   a, b
//	cmovb b, a                ; b = a < n ? a : n, so entry n is the default
//	mov   a, table            ; code base + table, a Reloc386Literal
//	add   a, [a+b*4]
//	jmp   a
//	table: dd target-table, ...
//
// The clamp is unconditional, so even a mispredicted bounds check loads an
// entry of the table. A is overwritten; the flags are written.

// jumpTableHeadSize is the size of the code before the table.
const jumpTableHeadSize = 5 + 2 + 3 + 5 + 3 + 2

// validJumpTable checks the registers and the table length of an
// OpJumpTable. ESP cannot be an index, and EBP needs a displacement as a
// base.
func validJumpTable(ins *machine.Instruction) bool {
	return len(ins.Table) >= 2 && uint64(len(ins.Table)-1) <= 0xffffffff && validGeneralRegisters(ins.A, ins.B) && ins.A != ins.B &&
		ins.A != 4 && ins.B != 4 && ins.A != 5
}

// JumpTable implements OpJumpTable.
func (e *ByteAssembler) JumpTable(ins machine.Instruction) error {
	if !e.require(validJumpTable(&ins)) {
		return e.err
	}
	a, b := byte(ins.A), byte(ins.B)
	start := e.pc + jumpTableHeadSize
	for _, target := range ins.Table {
		if !e.require(int64(target-start) == int64(int32(target-start))) {
			return e.err
		}
	}
	code := append32(append(e.code, 0xb8|b), uint32(len(ins.Table)-1)) // MOV b, n
	code = append(code, 0x39, 0xc0|b<<3|a)                             // CMP a, b
	code = append(code, 0x0f, 0x42, 0xc0|b<<3|a)                       // CMOVB b, a
	e.relocations = append(e.relocations, machine.Relocation{Encoding: machine.Reloc386Literal, Offset: len(code), TargetOffset: uint64(start)})
	code = append32(append(code, 0xb8|a), 0)       // MOV a, code base + table
	code = append(code, 0x03, a<<3|4, 0x80|b<<3|a) // ADD a, [a+b*4]
	code = append(code, 0xff, 0xe0|a)              // JMP a
	for _, target := range ins.Table {
		code = append32(code, uint32(int32(target-start)))
	}
	e.code = code
	return nil
}

// JumpTable records the sequence ByteAssembler.JumpTable encodes.
func (e *TextAssembler) JumpTable(ins machine.Instruction) error {
	if !validJumpTable(&ins) {
		return e.ByteAssembler.JumpTable(ins)
	}
	a, b := name(ins.A), name(ins.B)
	start := e.pc + jumpTableHeadSize
	e.record("mov %s, 0x%x", b, len(ins.Table)-1)
	e.record("cmp %s, %s", a, b)
	e.record("cmovb %s, %s", b, a)
	e.record("mov %s, literal + 0x%x", a, start)
	e.record("add %s, [%s+%s*4]", a, a, b)
	e.record("jmp %s", a)
	for _, target := range ins.Table {
		e.record(".long 0x%x-0x%x", target, start)
	}
	return e.ByteAssembler.JumpTable(ins)
}
