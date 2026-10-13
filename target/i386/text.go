package i386

import (
	"fmt"
	"strings"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
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
func name(reg machine.Register) string {
	return [...]string{"EAX", "ECX", "EDX", "EBX", "ESP", "EBP", "ESI", "EDI"}[reg]
}
func arithName(op ir.Opcode) string {
	return map[ir.Opcode]string{ir.OpAdd: "add", ir.OpSub: "sub", ir.OpMul: "imul", ir.OpAnd: "and", ir.OpOr: "or", ir.OpXor: "xor"}[op]
}
func shiftName(op ir.Opcode) string {
	return map[ir.Opcode]string{ir.OpShiftLeft: "shl", ir.OpShiftRight: "shr", ir.OpShiftRightArithmetic: "sar", ir.OpRotateLeft: "rol", ir.OpRotateRight: "ror"}[op]
}
func (e *TextAssembler) Constant(dst machine.Register, value uint64) {
	e.record("mov %s, 0x%x", name(dst), uint32(value))
	e.ByteAssembler.Constant(dst, value)
}
func (e *TextAssembler) Symbol(dst machine.Register, symbol string) {
	e.record("mov %s, %s", name(dst), symbol)
	e.ByteAssembler.Symbol(dst, symbol)
}
func (e *TextAssembler) Move(dst, src machine.Register) {
	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	e.ByteAssembler.Move(dst, src)
}
func (e *TextAssembler) Binary(op ir.Opcode, width uint8, dst, left, right machine.Register) {
	if dst != left {
		e.record("mov %s, %s", name(dst), name(left))
	}
	e.record("%s %s, %s", arithName(op), name(dst), name(right))
	if width == 8 || width == 16 {
		e.record("and %s, 0x%x", name(dst), uint32(1<<width)-1)
	}
	e.ByteAssembler.Binary(op, width, dst, left, right)
}
func (e *TextAssembler) BinaryImmediate(op ir.Opcode, width uint8, dst, left machine.Register, value uint64) {
	if dst != left {
		e.record("mov %s, %s", name(dst), name(left))
	}
	e.record("%s %s, 0x%x", arithName(op), name(dst), uint32(value))
	e.ByteAssembler.BinaryImmediate(op, width, dst, left, value)
}
func (e *TextAssembler) BinaryCarry(operation ir.Opcode, dst, left, right machine.Register) {
	e.WordCarry(machine.Instruction{Op: machine.OpWordCarry, Arithmetic: operation, Bits: 32, A: dst, B: left, C: right, CarryMode: machine.CarryFlags})
}

func (e *TextAssembler) MulWide(right machine.Register) {
	e.record("mul %s", name(right))
	e.ByteAssembler.MulWide(right)
}
func (e *TextAssembler) Decrement(dst, src machine.Register) {
	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	e.record("dec %s", name(dst))
	e.ByteAssembler.Decrement(dst, src)
}
func (e *TextAssembler) Increment(dst, src machine.Register) {
	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	e.record("inc %s", name(dst))
	e.ByteAssembler.Increment(dst, src)
}
func (e *TextAssembler) Narrow(dst, src machine.Register, width uint8) {
	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	if width == 8 || width == 16 {
		e.record("and %s, 0x%x", name(dst), uint32(1<<width)-1)
	}
	e.ByteAssembler.Narrow(dst, src, width)
}
func (e *TextAssembler) SignExtend(dst, src machine.Register, width uint8) {
	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	if width == 8 || width == 16 {
		e.record("shl %s, %d", name(dst), 32-width)
		e.record("sar %s, %d", name(dst), 32-width)
	}
	e.ByteAssembler.SignExtend(dst, src, width)
}
func (e *TextAssembler) Shift(op ir.Opcode, width uint8, dst, src machine.Register, count uint8) {
	if dst != src {
		e.record("mov %s, %s", name(dst), name(src))
	}
	if width < 32 && op == ir.OpShiftRightArithmetic {
		e.record("shl %s, %d", name(dst), 32-width)
		e.record("sar %s, %d", name(dst), 32-width)
	}
	e.record("%s %s, %d", shiftName(op), name(dst), count)
	if width < 32 && op != ir.OpShiftRightArithmetic {
		e.record("and %s, 0x%x", name(dst), uint32(1<<width)-1)
	}
	e.ByteAssembler.Shift(op, width, dst, src, count)
}
func address(base, index machine.Register, scale, disp uint64) string {
	return fmt.Sprintf("[%s + %s*%d + %d]", name(base), name(index), scale, int32(disp))
}
func offset(base machine.Register, disp uint64) string {
	return fmt.Sprintf("[%s + %d]", name(base), int32(disp))
}
func (e *TextAssembler) Scaled(dst, base, index machine.Register, scale, disp uint64) {
	e.record("lea %s, %s", name(dst), address(base, index, scale, disp))
	e.ByteAssembler.Scaled(dst, base, index, scale, disp)
}
func (e *TextAssembler) SelectValue(dst, condition, ifTrue, ifFalse machine.Register) {
	if dst != ifFalse {
		e.record("mov %s, %s", name(dst), name(ifFalse))
	}
	e.record("test %s, %s", name(condition), name(condition))
	e.record("cmovne %s, %s", name(dst), name(ifTrue))
	e.ByteAssembler.SelectValue(dst, condition, ifTrue, ifFalse)
}
func (e *TextAssembler) Load64(dst, addr machine.Register) {
	e.record("mov %s, dword ptr [%s]", name(dst), name(addr))
	e.ByteAssembler.Load64(dst, addr)
}
func (e *TextAssembler) Store64(addr, value machine.Register) {
	e.record("mov dword ptr [%s], %s", name(addr), name(value))
	e.ByteAssembler.Store64(addr, value)
}
func (e *TextAssembler) Load8(dst, addr machine.Register) {
	e.record("movzx %s, byte ptr [%s]", name(dst), name(addr))
	e.ByteAssembler.Load8(dst, addr)
}
func (e *TextAssembler) Store8(addr, value machine.Register) {
	e.record("mov byte ptr [%s], %s", name(addr), [...]string{"AL", "CL", "DL", "BL"}[value])
	e.ByteAssembler.Store8(addr, value)
}
func (e *TextAssembler) LoadAddress(dst, base, index machine.Register, scale, disp uint64) {
	e.record("mov %s, dword ptr %s", name(dst), address(base, index, scale, disp))
	e.ByteAssembler.LoadAddress(dst, base, index, scale, disp)
}
func (e *TextAssembler) StoreAddress(base, index, value machine.Register, scale, disp uint64) {
	e.record("mov dword ptr %s, %s", address(base, index, scale, disp), name(value))
	e.ByteAssembler.StoreAddress(base, index, value, scale, disp)
}
func (e *TextAssembler) LoadOffset(dst, base machine.Register, disp uint64) {
	e.record("mov %s, dword ptr %s", name(dst), offset(base, disp))
	e.ByteAssembler.LoadOffset(dst, base, disp)
}
func (e *TextAssembler) StoreOffset(base, value machine.Register, disp uint64) {
	e.record("mov dword ptr %s, %s", offset(base, disp), name(value))
	e.ByteAssembler.StoreOffset(base, value, disp)
}
func (e *TextAssembler) Syscall()             { e.record("int 0x80"); e.ByteAssembler.Syscall() }
func (e *TextAssembler) CallC()               { e.record("call EAX"); e.ByteAssembler.CallC() }
func (e *TextAssembler) CallGo()              { e.record("call EAX"); e.ByteAssembler.CallGo() }
func (e *TextAssembler) SaveLink(slot int)    { e.ByteAssembler.SaveLink(slot) }
func (e *TextAssembler) RestoreLink(slot int) { e.ByteAssembler.RestoreLink(slot) }
func (e *TextAssembler) Spill(src machine.Register, slot int) {
	base := machine.Register(5)
	if e.stack == abi.StackPlatform {
		base = 4
	}
	e.record("mov dword ptr %s, %s", offset(base, uint64(e.callArea+slot*4)), name(src))
	e.ByteAssembler.Spill(src, slot)
}
func (e *TextAssembler) Reload(dst machine.Register, slot int) {
	base := machine.Register(5)
	if e.stack == abi.StackPlatform {
		base = 4
	}
	e.record("mov %s, dword ptr %s", name(dst), offset(base, uint64(e.callArea+slot*4)))
	e.ByteAssembler.Reload(dst, slot)
}
func (e *TextAssembler) IncomingWord(dst machine.Register, entrySPOffset int) {
	e.record("mov %s, dword ptr [ESP + %d]", name(dst), e.frame+entrySPOffset)
	e.ByteAssembler.IncomingWord(dst, entrySPOffset)
}
func (e *TextAssembler) FrameEnter(frame int) { e.FrameEnterArea(frame, 0) }
func (e *TextAssembler) FrameEnterArea(frame, callArea int) {
	e.record("push EBX; push ESI; push EDI; push EBP; sub ESP, %d", frame-16)
	e.ByteAssembler.FrameEnterArea(frame, callArea)
}
func (e *TextAssembler) FrameLeave(frame int) {
	e.record("add ESP, %d; pop EBP; pop EDI; pop ESI; pop EBX", frame-16)
	e.ByteAssembler.FrameLeave(frame)
}
func (e *TextAssembler) Ret() { e.record("ret"); e.ByteAssembler.Ret() }
func (e *TextAssembler) CompareImmediate(reg machine.Register, value int64) {
	e.record("cmp %s, %d", name(reg), int32(value))
	e.ByteAssembler.CompareImmediate(reg, value)
}
func (e *TextAssembler) CompareRegister(left, right machine.Register) {
	e.record("cmp %s, %s", name(left), name(right))
	e.ByteAssembler.CompareRegister(left, right)
}
func (e *TextAssembler) Jump(target int) { e.record("jmp 0x%x", target); e.ByteAssembler.Jump(target) }
func (e *TextAssembler) JumpIf(condition ir.BranchCondition, target int) {
	mnemonic := [...]string{"je", "jnz", "jl", "jge", "jle", "jg", "jb", "jae", "jbe", "ja", "js", "jns"}[condition]
	e.record("%s 0x%x", mnemonic, target)
	e.ByteAssembler.JumpIf(condition, target)
}
