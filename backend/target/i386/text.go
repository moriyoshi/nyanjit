package i386

import (
	"fmt"
	"strings"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// TextAssembler mirrors the byte encoder while recording readable x86 syntax.
type TextAssembler struct {
	*ByteAssembler
	lines []string
}

func NewTextAssembler(stack abi.StackPractice) *TextAssembler {
	return &TextAssembler{ByteAssembler: NewByteAssembler(stack)}
}
func (e *TextAssembler) Text() string { return strings.Join(e.lines, "\n") + "\n" }
func (e *TextAssembler) record(format string, args ...any) {
	e.lines = append(e.lines, fmt.Sprintf(format, args...))
}
func name(reg backend.Register) string {
	return [...]string{"EAX", "ECX", "EDX", "EBX", "ESP", "EBP", "ESI", "EDI"}[reg]
}
func arithName(operation op.Opcode) string {
	return map[op.Opcode]string{op.OpAdd: "add", op.OpSub: "sub", op.OpMul: "imul", op.OpAnd: "and", op.OpOr: "or", op.OpXor: "xor"}[operation]
}
func shiftName(operation op.Opcode) string {
	switch operation {
	case op.OpShiftLeft:
		return "shl"
	case op.OpShiftRight:
		return "shr"
	case op.OpShiftRightArithmetic:
		return "sar"
	case op.OpRotateLeft:
		return "rol"
	case op.OpRotateRight:
		return "ror"
	}
	return ""
}
func (e *TextAssembler) Constant(dst backend.Register, value uint64) {
	if !e.require(validGeneralRegisters(dst) && fitsWord(value)) {
		return
	}

	e.record("mov %s, 0x%x", name(dst), uint32(value))
	e.ByteAssembler.Constant(dst, value)
}
func (e *TextAssembler) Symbol(dst backend.Register, symbol string) {
	if !e.require(validGeneralRegisters(dst)) {
		return
	}

	e.record("mov %s, %s", name(dst), symbol)
	e.ByteAssembler.Symbol(dst, symbol)
}
func (e *TextAssembler) Move(dst, src backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	e.ByteAssembler.Move(dst, src)
}
func (e *TextAssembler) Binary(op op.Opcode, width uint8, dst, left, right backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(left) && validGeneralRegisters(right) && validBinary(op, width)) {
		return
	}

	if dst != left {
		e.record("mov %s, %s", name(dst), name(left))
	}
	e.record("%s %s, %s", arithName(op), name(dst), name(right))
	if width == 8 || width == 16 {
		e.record("and %s, 0x%x", name(dst), uint32(1<<width)-1)
	}
	e.ByteAssembler.Binary(op, width, dst, left, right)
}
func (e *TextAssembler) BinaryImmediate(op op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(left) && validBinaryImmediate(op, width, value)) {
		return
	}

	if dst != left {
		e.record("mov %s, %s", name(dst), name(left))
	}
	e.record("%s %s, 0x%x", arithName(op), name(dst), uint32(value))
	e.ByteAssembler.BinaryImmediate(op, width, dst, left, value)
}
func (e *TextAssembler) BinaryCarry(operation op.Opcode, dst, left, right backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(left) && validGeneralRegisters(right)) {
		return
	}

	e.WordCarry(machine.Instruction{Op: machine.OpWordCarry, Arithmetic: operation, Bits: 32, A: dst, B: left, C: right, CarryMode: machine.CarryFlags})
}

func (e *TextAssembler) MulWide(right backend.Register) {
	if !e.require(validGeneralRegisters(right)) {
		return
	}

	e.record("mul %s", name(right))
	e.ByteAssembler.MulWide(right)
}
func (e *TextAssembler) Decrement(dst, src backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	e.record("dec %s", name(dst))
	e.ByteAssembler.Decrement(dst, src)
}
func (e *TextAssembler) Increment(dst, src backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	e.record("inc %s", name(dst))
	e.ByteAssembler.Increment(dst, src)
}
func (e *TextAssembler) Narrow(dst, src backend.Register, width uint8) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	if width == 8 || width == 16 {
		e.record("and %s, 0x%x", name(dst), uint32(1<<width)-1)
	}
	e.ByteAssembler.Narrow(dst, src, width)
}
func (e *TextAssembler) SignExtend(dst, src backend.Register, width uint8) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	if width == 8 || width == 16 {
		e.record("shl %s, %d", name(dst), 32-width)
		e.record("sar %s, %d", name(dst), 32-width)
	}
	e.ByteAssembler.SignExtend(dst, src, width)
}
func (e *TextAssembler) Shift(operation op.Opcode, width uint8, dst, src backend.Register, count uint8) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	if width < 32 && operation == op.OpShiftRightArithmetic {
		e.record("shl %s, %d", name(dst), 32-width)
		e.record("sar %s, %d", name(dst), 32-width)
	}
	e.record("%s %s, %d", shiftName(operation), name(dst), count)
	if width < 32 && operation != op.OpShiftRightArithmetic {
		e.record("and %s, 0x%x", name(dst), uint32(1<<width)-1)
	}
	e.ByteAssembler.Shift(operation, width, dst, src, count)
}
func address(base, index backend.Register, scale, disp uint64) string {
	return fmt.Sprintf("[%s + %s*%d + %d]", name(base), name(index), scale, int32(disp))
}
func offset(base backend.Register, disp uint64) string {
	return fmt.Sprintf("[%s + %d]", name(base), int32(disp))
}
func (e *TextAssembler) Scaled(dst, base, index backend.Register, scale, disp uint64) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(base) && validGeneralRegisters(index) && validIndexed(index, scale, disp)) {
		return
	}

	e.record("lea %s, %s", name(dst), address(base, index, scale, disp))
	e.ByteAssembler.Scaled(dst, base, index, scale, disp)
}
func (e *TextAssembler) SelectValue(dst, condition, ifTrue, ifFalse backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(condition) && validGeneralRegisters(ifTrue) && validGeneralRegisters(ifFalse)) {
		return
	}

	if dst != ifFalse {
		e.record("mov %s, %s", name(dst), name(ifFalse))
	}
	e.record("test %s, %s", name(condition), name(condition))
	e.record("cmovne %s, %s", name(dst), name(ifTrue))
	e.ByteAssembler.SelectValue(dst, condition, ifTrue, ifFalse)
}
func (e *TextAssembler) Load64(dst, addr backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(addr)) {
		return
	}

	e.record("mov %s, dword ptr [%s]", name(dst), name(addr))
	e.ByteAssembler.Load64(dst, addr)
}
func (e *TextAssembler) Store64(addr, value backend.Register) {
	if !e.require(validGeneralRegisters(addr) && validGeneralRegisters(value)) {
		return
	}

	e.record("mov dword ptr [%s], %s", name(addr), name(value))
	e.ByteAssembler.Store64(addr, value)
}
func (e *TextAssembler) Load8(dst, addr backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(addr)) {
		return
	}

	e.record("movzx %s, byte ptr [%s]", name(dst), name(addr))
	e.ByteAssembler.Load8(dst, addr)
}
func (e *TextAssembler) Store8(addr, value backend.Register) {
	if !e.require(validGeneralRegisters(addr) && validGeneralRegisters(value) && value < 4) {
		return
	}

	e.record("mov byte ptr [%s], %s", name(addr), [...]string{"AL", "CL", "DL", "BL"}[value])
	e.ByteAssembler.Store8(addr, value)
}
func (e *TextAssembler) LoadAddress(dst, base, index backend.Register, scale, disp uint64) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(base) && validGeneralRegisters(index) && validIndexed(index, scale, disp)) {
		return
	}

	e.record("mov %s, dword ptr %s", name(dst), address(base, index, scale, disp))
	e.ByteAssembler.LoadAddress(dst, base, index, scale, disp)
}
func (e *TextAssembler) StoreAddress(base, index, value backend.Register, scale, disp uint64) {
	if !e.require(validGeneralRegisters(base) && validGeneralRegisters(index) && validGeneralRegisters(value) && validIndexed(index, scale, disp)) {
		return
	}

	e.record("mov dword ptr %s, %s", address(base, index, scale, disp), name(value))
	e.ByteAssembler.StoreAddress(base, index, value, scale, disp)
}
func (e *TextAssembler) LoadOffset(dst, base backend.Register, disp uint64) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(base) && fitsWord(disp)) {
		return
	}

	e.record("mov %s, dword ptr %s", name(dst), offset(base, disp))
	e.ByteAssembler.LoadOffset(dst, base, disp)
}
func (e *TextAssembler) StoreOffset(base, value backend.Register, disp uint64) {
	if !e.require(validGeneralRegisters(base) && validGeneralRegisters(value) && fitsWord(disp)) {
		return
	}

	e.record("mov dword ptr %s, %s", offset(base, disp), name(value))
	e.ByteAssembler.StoreOffset(base, value, disp)
}
func (e *TextAssembler) Syscall()             { e.record("int 0x80"); e.ByteAssembler.Syscall() }
func (e *TextAssembler) CallC()               { e.record("call EAX"); e.ByteAssembler.CallC() }
func (e *TextAssembler) CallGo()              { e.record("call EAX"); e.ByteAssembler.CallGo() }
func (e *TextAssembler) SaveLink(slot int)    { e.ByteAssembler.SaveLink(slot) }
func (e *TextAssembler) RestoreLink(slot int) { e.ByteAssembler.RestoreLink(slot) }
func (e *TextAssembler) Spill(src backend.Register, slot int) {
	if !e.require(validGeneralRegisters(src)) {
		return
	}

	base := backend.Register(5)
	if e.stack == abi.StackPlatform {
		base = 4
	}
	e.record("mov dword ptr %s, %s", offset(base, uint64(e.callArea+slot*4)), name(src))
	e.ByteAssembler.Spill(src, slot)
}
func (e *TextAssembler) Reload(dst backend.Register, slot int) {
	if !e.require(validGeneralRegisters(dst)) {
		return
	}

	base := backend.Register(5)
	if e.stack == abi.StackPlatform {
		base = 4
	}
	e.record("mov %s, dword ptr %s", name(dst), offset(base, uint64(e.callArea+slot*4)))
	e.ByteAssembler.Reload(dst, slot)
}
func (e *TextAssembler) IncomingWord(dst backend.Register, entrySPOffset int) {
	if !e.require(validGeneralRegisters(dst)) {
		return
	}

	e.record("mov %s, dword ptr [ESP + %d]", name(dst), e.frame+entrySPOffset)
	e.ByteAssembler.IncomingWord(dst, entrySPOffset)
}
func (e *TextAssembler) FrameEnter(frame int) { e.FrameEnterArea(frame, 0) }
func (e *TextAssembler) FrameEnterArea(frame, callArea int) {
	if !e.require(frame >= 16 && callArea >= 0) {
		return
	}
	e.record("push EBX; push ESI; push EDI; push EBP; sub ESP, %d", frame-16)
	e.ByteAssembler.FrameEnterArea(frame, callArea)
}
func (e *TextAssembler) FrameLeave(frame int) {
	if !e.require(frame >= 16) {
		return
	}
	e.record("add ESP, %d; pop EBP; pop EDI; pop ESI; pop EBX", frame-16)
	e.ByteAssembler.FrameLeave(frame)
}
func (e *TextAssembler) Ret() { e.record("ret"); e.ByteAssembler.Ret() }
func (e *TextAssembler) CompareImmediate(reg backend.Register, value int64) {
	if !e.require(validGeneralRegisters(reg)) {
		return
	}

	e.record("cmp %s, %d", name(reg), int32(value))
	e.ByteAssembler.CompareImmediate(reg, value)
}
func (e *TextAssembler) CompareRegister(left, right backend.Register) {
	if !e.require(validGeneralRegisters(left) && validGeneralRegisters(right)) {
		return
	}

	e.record("cmp %s, %s", name(left), name(right))
	e.ByteAssembler.CompareRegister(left, right)
}
func (e *TextAssembler) Jump(target int) { e.record("jmp 0x%x", target); e.ByteAssembler.Jump(target) }
func (e *TextAssembler) JumpIf(condition op.BranchCondition, target int) {
	if !e.require(condition < 12) {
		return
	}
	mnemonic := [...]string{"je", "jnz", "jl", "jge", "jle", "jg", "jb", "jae", "jbe", "ja", "js", "jns"}[condition]
	e.record("%s 0x%x", mnemonic, target)
	e.ByteAssembler.JumpIf(condition, target)
}
