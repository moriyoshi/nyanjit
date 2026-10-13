package amd64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"strings"
)

func NewTextAssembler(stack abi.StackPractice, goHomes int) *TextAssembler {
	return &TextAssembler{stack: stack, goHomes: goHomes}
}

var amd64RegisterNames = [...]string{"RAX", "RCX", "RDX", "RBX", "RSP", "RBP", "RSI", "RDI", "R8", "R9", "R10", "R11", "R12", "R13", "R14", "R15"}
var amd64RegisterNames32 = [...]string{"EAX", "ECX", "EDX", "EBX", "ESP", "EBP", "ESI", "EDI", "R8D", "R9D", "R10D", "R11D", "R12D", "R13D", "R14D", "R15D"}
var amd64RegisterNames16 = [...]string{"AX", "CX", "DX", "BX", "SP", "BP", "SI", "DI", "R8W", "R9W", "R10W", "R11W", "R12W", "R13W", "R14W", "R15W"}
var amd64RegisterNames8 = [...]string{"AL", "CL", "DL", "BL", "SPL", "BPL", "SIL", "DIL", "R8B", "R9B", "R10B", "R11B", "R12B", "R13B", "R14B", "R15B"}

func amd64Name(reg machine.Register) string { return amd64RegisterNames[reg] }

func amd64Address(base, index machine.Register, scale, disp uint64, indexed bool) string {
	address := amd64Name(base)
	if indexed {
		address += fmt.Sprintf(" + %s*%d", amd64Name(index), scale)
	}
	if signed := int64(disp); signed > 0 {
		address += fmt.Sprintf(" + %d", signed)
	} else if signed < 0 {
		address += fmt.Sprintf(" - %d", -signed)
	}
	return "[" + address + "]"
}

// TextAssembler is independent of the byte assembler. Each visitor method
// appends target assembly without creating machine code.
type TextAssembler struct {
	lines   []string
	pc      int
	stack   abi.StackPractice
	goHomes int
	profile abi.CPUProfile
}

func (e *TextAssembler) SetPC(pc int)       { e.pc = pc }
func (e *TextAssembler) AssembledSize() int { return e.pc }
func (e *TextAssembler) Text() string       { return strings.Join(e.lines, "\n") + "\n" }
func (e *TextAssembler) line(size int, format string, args ...any) {
	e.lines = append(e.lines, fmt.Sprintf("%04x  %s", e.pc, fmt.Sprintf(format, args...)))
	e.pc += size
}
func (e *TextAssembler) Constant(dst machine.Register, value uint64) {
	name := amd64Name(dst)
	if value <= 0xffffffff {
		name = amd64RegisterNames32[dst]
	}
	e.line(ConstantSize(dst, value), "mov %s, 0x%x", name, value)
}
func (e *TextAssembler) Symbol(dst machine.Register, name string) {
	e.line(10, "movabs %s, <%s>", amd64Name(dst), name)
}
func (e *TextAssembler) Move(dst, src machine.Register) {
	if dst != src {
		e.line(3, "mov %s, %s", amd64Name(dst), amd64Name(src))
	}
}
func (e *TextAssembler) Binary(operation ir.Opcode, width uint8, dst, left, right machine.Register) {
	e.Move(dst, left)
	mnemonic := map[ir.Opcode]string{ir.OpAdd: "add", ir.OpSub: "sub", ir.OpMul: "imul", ir.OpAnd: "and", ir.OpOr: "or", ir.OpXor: "xor"}[operation]
	size := 3
	name := amd64Name
	if width == 8 && operation == ir.OpMul {
		width = 64
	}
	switch width {
	case 8:
		name = func(reg machine.Register) string { return amd64RegisterNames8[reg] }
	case 16:
		name = func(reg machine.Register) string { return amd64RegisterNames16[reg] }
		size++
	case 32:
		name = func(reg machine.Register) string { return amd64RegisterNames32[reg] }
	}
	if operation == ir.OpMul {
		size = 4
		if width == 16 {
			size = 5
		}
	}
	e.line(size, "%s %s, %s", mnemonic, name(dst), name(right))
}
func (e *TextAssembler) BinaryImmediate(operation ir.Opcode, width uint8, dst, left machine.Register, value uint64) {
	e.Move(dst, left)
	mnemonic := map[ir.Opcode]string{ir.OpAdd: "add", ir.OpSub: "sub", ir.OpAnd: "and", ir.OpOr: "or", ir.OpXor: "xor"}[operation]
	size := 7
	if int64(value) >= -128 && int64(value) <= 127 {
		size = 4
	}
	name := amd64Name(dst)
	if width < 64 {
		name = amd64RegisterNames32[dst]
	}
	e.line(size, "%s %s, %d", mnemonic, name, int64(value))
}
func (e *TextAssembler) Decrement(dst, src machine.Register) {
	e.Move(dst, src)
	e.line(3, "dec %s", amd64Name(dst))
}
func (e *TextAssembler) Increment(dst, src machine.Register) {
	e.Move(dst, src)
	e.line(3, "inc %s", amd64Name(dst))
}
func (e *TextAssembler) Narrow(dst, src machine.Register, width uint8) {
	switch width {
	case 8:
		e.line(4, "movzx %s, %s", amd64RegisterNames32[dst], amd64RegisterNames8[src])
	case 16:
		e.line(4, "movzx %s, %s", amd64RegisterNames32[dst], amd64RegisterNames16[src])
	case 32:
		e.line(3, "mov %s, %s", amd64RegisterNames32[dst], amd64RegisterNames32[src])
	}
}
func (e *TextAssembler) SignExtend(dst, src machine.Register, width uint8) {
	switch width {
	case 8:
		e.line(4, "movsx %s, %s", amd64Name(dst), amd64RegisterNames8[src])
	case 16:
		e.line(4, "movsx %s, %s", amd64Name(dst), amd64RegisterNames16[src])
	case 32:
		e.line(3, "movsxd %s, %s", amd64Name(dst), amd64RegisterNames32[src])
	}
}
func (e *TextAssembler) Shift(operation ir.Opcode, width uint8, dst, src machine.Register, count uint8) {
	if code := rorxPlan(operation, width, dst, src, count, e.profile); code != nil {
		name := amd64Name
		if width == 32 {
			name = func(r machine.Register) string { return amd64RegisterNames32[r] }
		}
		e.line(len(code), "rorx %s, %s, %d", name(dst), name(src), code[len(code)-1])
		return
	}
	if dst != src {
		e.Move(dst, src)
	}
	name := amd64Name(dst)
	switch width {
	case 8:
		name = amd64RegisterNames8[dst]
	case 16:
		name = amd64RegisterNames16[dst]
	case 32:
		name = amd64RegisterNames32[dst]
	}
	mnemonic := map[ir.Opcode]string{ir.OpShiftLeft: "shl", ir.OpShiftRight: "shr", ir.OpShiftRightArithmetic: "sar", ir.OpRotateLeft: "rol", ir.OpRotateRight: "ror"}[operation]
	size := 4
	if width == 16 {
		size++
	}
	e.line(size, "%s %s, %d", mnemonic, name, count)
}
func (e *TextAssembler) Scaled(dst, base, index machine.Register, scale, disp uint64) {
	e.line(IndexedSize(base, disp), "lea %s, %s", amd64Name(dst), amd64Address(base, index, scale, disp, true))
}
func (e *TextAssembler) SelectValue(dst, condition, ifTrue, ifFalse machine.Register) {
	e.Move(dst, ifFalse)
	e.line(3, "test %s, %s", amd64Name(condition), amd64Name(condition))
	e.line(4, "cmovne %s, %s", amd64Name(dst), amd64Name(ifTrue))
}
func (e *TextAssembler) Load64(dst, address machine.Register) {
	e.line(MemorySize(address), "mov %s, qword ptr [%s]", amd64Name(dst), amd64Name(address))
}
func (e *TextAssembler) Store64(address, value machine.Register) {
	e.line(MemorySize(address), "mov qword ptr [%s], %s", amd64Name(address), amd64Name(value))
}
func (e *TextAssembler) Load8(dst, address machine.Register) {
	e.line(MemorySize(address)+1, "movzx %s, byte ptr [%s]", amd64Name(dst), amd64Name(address))
}
func (e *TextAssembler) Store8(address, value machine.Register) {
	e.line(MemorySize(address), "mov byte ptr [%s], %s", amd64Name(address), amd64RegisterNames8[value])
}
func (e *TextAssembler) LoadAddress(dst, base, index machine.Register, scale, disp uint64) {
	e.line(IndexedSize(base, disp), "mov %s, qword ptr %s", amd64Name(dst), amd64Address(base, index, scale, disp, true))
}
func (e *TextAssembler) StoreAddress(base, index, value machine.Register, scale, disp uint64) {
	e.line(IndexedSize(base, disp), "mov qword ptr %s, %s", amd64Address(base, index, scale, disp, true), amd64Name(value))
}
func (e *TextAssembler) LoadOffset(dst, base machine.Register, disp uint64) {
	e.line(OffsetSize(base, disp), "mov %s, qword ptr %s", amd64Name(dst), amd64Address(base, 0, 0, disp, false))
}
func (e *TextAssembler) StoreOffset(base, value machine.Register, disp uint64) {
	e.line(OffsetSize(base, disp), "mov qword ptr %s, %s", amd64Address(base, 0, 0, disp, false), amd64Name(value))
}
func (e *TextAssembler) Syscall() {
	e.line(2, "syscall")
}
func (e *TextAssembler) CallC() {
	e.line(2, "xor EAX, EAX")
	e.line(3, "call R11")
}
func (e *TextAssembler) CallGo()       { e.line(3, "call R11") }
func (*TextAssembler) SaveLink(int)    {}
func (*TextAssembler) RestoreLink(int) {}
func (e *TextAssembler) Spill(src machine.Register, slot int) {
	base := machine.Register(10)
	size := 7
	if e.stack == abi.StackPlatform {
		base = 4
		size = 8
		slot += e.goHomes
	}
	e.line(size, "mov qword ptr [%s + %d], %s", amd64Name(base), slot*8, amd64Name(src))
}
func (e *TextAssembler) Reload(dst machine.Register, slot int) {
	base := machine.Register(10)
	size := 7
	if e.stack == abi.StackPlatform {
		base = 4
		size = 8
		slot += e.goHomes
	}
	e.line(size, "mov %s, qword ptr [%s + %d]", amd64Name(dst), amd64Name(base), slot*8)
}
func (e *TextAssembler) FrameEnter(frame int) { e.line(7, "sub RSP, %d", frame) }
func (e *TextAssembler) FrameLeave(frame int) { e.line(7, "add RSP, %d", frame) }
func (e *TextAssembler) Ret()                 { e.line(1, "ret") }
func (e *TextAssembler) CompareImmediate(reg machine.Register, value int64) {
	e.line(7, "cmp %s, %d", amd64Name(reg), value)
}
func (e *TextAssembler) CompareRegister(left, right machine.Register) {
	e.line(3, "cmp %s, %s", amd64Name(left), amd64Name(right))
}
func (e *TextAssembler) Jump(target int) { e.line(5, "jmp 0x%x", target) }
func (e *TextAssembler) JumpIf(condition ir.BranchCondition, target int) {
	mnemonic := [...]string{"je", "jnz", "jl", "jge", "jle", "jg", "jb", "jae", "jbe", "ja", "js", "jns"}[condition]
	e.line(6, "%s 0x%x", mnemonic, target)
}

func (e *TextAssembler) BranchZero(_ machine.Register, _ uint8, _ bool, target int, long bool) {
	if !long {
		e.line(2, "jrcxz 0x%x", target)
		return
	}
	e.line(2, "jrcxz 0x%x", e.pc+7)
	e.line(5, "jmp 0x%x", e.pc+10)
	e.line(5, "jmp 0x%x", target)
}

func (e *TextAssembler) Loop(target int, long bool) {
	if !long {
		e.line(2, "loop 0x%x", target)
		return
	}
	e.line(4, "lea RCX, [RCX - 1]")
	e.line(2, "jrcxz 0x%x", e.pc+7)
	e.line(5, "jmp 0x%x", target)
}

// WithCPUProfile selects validated instruction extensions for text emission.
func (e *TextAssembler) WithCPUProfile(p abi.CPUProfile) *TextAssembler {
	e.profile = p.Clone()
	return e
}
