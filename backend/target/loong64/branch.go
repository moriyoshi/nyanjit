package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func (e *ByteAssembler) Ret()     { e.emit(0x4c000020, "jirl r0, r1, 0") }
func (e *ByteAssembler) Syscall() { e.emit(0x002b0000, "syscall 0") }
func (e *ByteAssembler) CallC()   { e.emit(0x4c000281, "jirl r1, r20, 0") }
func (e *ByteAssembler) CallGo()  { e.CallC() }
func (e *ByteAssembler) Compare(i machine.Instruction) error {
	if !e.require((validInstructionRegisters(i))) {
		return e.err
	}

	if i.Bits != 0 && i.Bits != 64 {
		return machine.ErrUnsupportedOperation
	}
	if i.Op == machine.OpCompareImmediate {
		e.CompareImmediate(i.A, int64(i.Imm))
	} else {
		e.CompareRegister(i.A, i.B)
	}
	return nil
}
func (e *ByteAssembler) CompareImmediate(r backend.Register, v int64) {
	if !e.require((validGeneralRegisters(r))) {
		return
	}

	e.left, e.right = r, 0
	if v != 0 {
		e.Constant(20, uint64(v))
		e.right = 20
	}
}
func (e *ByteAssembler) CompareRegister(a, b backend.Register) {
	if !e.require((validGeneralRegisters(a, b))) {
		return
	}
	e.left, e.right = a, b
}
func branch(c op.BranchCondition, a, b backend.Register) (uint32, string, backend.Register, backend.Register) {
	switch c {
	case op.BranchEqual:
		return 0x58000000, "beq", a, b
	case op.BranchNotEqual:
		return 0x5c000000, "bne", a, b
	case op.BranchLess:
		return 0x60000000, "blt", a, b
	case op.BranchGreaterEqual:
		return 0x64000000, "bge", a, b
	case op.BranchLessEqual:
		return 0x64000000, "bge", b, a
	case op.BranchGreater:
		return 0x60000000, "blt", b, a
	case op.BranchBelow:
		return 0x68000000, "bltu", a, b
	case op.BranchAboveEqual:
		return 0x6c000000, "bgeu", a, b
	case op.BranchBelowEqual:
		return 0x6c000000, "bgeu", b, a
	case op.BranchAbove:
		return 0x68000000, "bltu", b, a
	}
	return 0x58000000, "beq", a, b
}
func (e *ByteAssembler) farJump(from, to int) {
	if !e.require(e.displacement(from, to, 32, 4) && int64(to)-int64(from) <= (1<<31)-2049) {
		return
	}
	v := int64(to) - int64(from)
	hi := (v + 2048) >> 12
	lo := v - hi<<12
	e.upper(0x1c000000, "pcaddu12i", 19, uint64(hi))
	e.emit(0x4c000000|(uint32(lo>>2)&0xffff)<<10|19<<5, "jirl r0, r19, %d", lo)
}
func (e *ByteAssembler) b(from, to int) {
	if !e.require(e.displacement(from, to, 28, 4)) {
		return
	}
	v := uint32((int64(to) - int64(from)) >> 2)
	e.emit(0x50000000|(v&0xffff)<<10|(v>>16&0x3ff), "b 0x%x", to)
}
func (e *ByteAssembler) Jump(to int) { e.JumpForm(to, false) }
func (e *ByteAssembler) JumpForm(to int, long bool) {
	if long {
		e.farJump(e.pc, to)
	} else {
		e.b(e.pc, to)
	}
}
func (e *ByteAssembler) JumpIf(c op.BranchCondition, to int) {
	if !e.require((c < 12)) {
		return
	}
	e.JumpIfRange(c, to, false, false)
}
func (e *ByteAssembler) JumpIfRange(c op.BranchCondition, to int, long, far bool) {
	if !e.require((c < 12)) {
		return
	}

	if !e.require(c <= 9 && (long || e.displacement(e.pc, to, 18, 4))) {
		return
	}
	op, name, a, b := branch(c, e.left, e.right)
	if !long {
		e.emit(op|(uint32((to-e.pc)>>2)&0xffff)<<10|uint32(a)<<5|uint32(b), "%s r%d, r%d, 0x%x", name, a, b, to)
		return
	}
	skip := 8
	if far {
		skip = 12
	}
	op ^= 0x04000000
	inv := invertedBranch(name)
	e.emit(op|uint32(skip/4)<<10|uint32(a)<<5|uint32(b), "%s r%d, r%d, 0x%x", inv, a, b, e.pc+skip)
	if far {
		e.farJump(e.pc+4, to)
	} else {
		e.b(e.pc+4, to)
	}
}
func (e *ByteAssembler) BranchZeroRange(r backend.Register, width uint8, nz bool, to int, long, far bool) {
	if !e.require((validGeneralRegisters(r)) && (width == 0 || validWidth(width))) {
		return
	}

	if !e.require(r >= 0 && r <= 31 && (long || e.displacement(e.pc, to, 23, 4))) {
		return
	}
	op, name := uint32(0x40000000), "beqz"
	if nz {
		op, name = 0x44000000, "bnez"
	}
	if !long {
		v := uint32((to - e.pc) >> 2)
		e.emit(op|(v&0xffff)<<10|uint32(r)<<5|(v>>16&31), "%s r%d, 0x%x", name, r, to)
		return
	}
	skip := 8
	if far {
		skip = 12
	}
	op ^= 0x04000000
	if nz {
		name = "beqz"
	} else {
		name = "bnez"
	}
	e.emit(op|uint32(skip/4)<<10|uint32(r)<<5, "%s r%d, 0x%x", name, r, e.pc+skip)
	if far {
		e.farJump(e.pc+4, to)
	} else {
		e.b(e.pc+4, to)
	}
}

// invertedBranch names the branch with the opposite condition.
func invertedBranch(name string) string {
	switch name {
	case "beq":
		return "bne"
	case "bne":
		return "beq"
	case "blt":
		return "bge"
	case "bge":
		return "blt"
	case "bltu":
		return "bgeu"
	case "bgeu":
		return "bltu"
	}
	return ""
}
