package amd64

import (
	"fmt"
	"strings"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func NewTextAssembler(stack abi.StackPractice, goHomes int) *TextAssembler {
	return &TextAssembler{stack: stack, goHomes: goHomes}
}

var amd64RegisterNames = [...]string{"RAX", "RCX", "RDX", "RBX", "RSP", "RBP", "RSI", "RDI", "R8", "R9", "R10", "R11", "R12", "R13", "R14", "R15"}
var amd64RegisterNames32 = [...]string{"EAX", "ECX", "EDX", "EBX", "ESP", "EBP", "ESI", "EDI", "R8D", "R9D", "R10D", "R11D", "R12D", "R13D", "R14D", "R15D"}
var amd64RegisterNames16 = [...]string{"AX", "CX", "DX", "BX", "SP", "BP", "SI", "DI", "R8W", "R9W", "R10W", "R11W", "R12W", "R13W", "R14W", "R15W"}
var amd64RegisterNames8 = [...]string{"AL", "CL", "DL", "BL", "SPL", "BPL", "SIL", "DIL", "R8B", "R9B", "R10B", "R11B", "R12B", "R13B", "R14B", "R15B"}

func amd64Name(reg backend.Register) string { return amd64RegisterNames[reg] }

func amd64Address(base, index backend.Register, scale, disp uint64, indexed bool) string {
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

// TextAssembler renders each visited operation as target assembly. A private
// byte encoder checks every operation first, and Err reports its rejection.
type TextAssembler struct {
	lines   []string
	pc      int
	stack   abi.StackPractice
	goHomes int
	check   *ByteAssembler
	profile abi.ResolvedCPU
}

func (e *TextAssembler) SetPC(pc int) {
	e.pc = pc
	e.encoder().SetPC(pc)
}

// Err reports the first operation the target's byte encoder rejects. A
// rejected operation adds no text, so a listing never shows code that native
// emission would refuse.
func (e *TextAssembler) Err() error { return e.encoder().Err() }

// encoder returns the private byte encoder that validates each operation with
// the same rules as native emission, at the listing's current pc.
func (e *TextAssembler) encoder() *ByteAssembler {
	if e.check == nil {
		e.check = NewByteAssembler(e.stack, e.goHomes).WithCPU(e.profile)
	}
	return e.check
}

func (e *TextAssembler) valid(op func(*ByteAssembler)) bool {
	b := e.encoder()
	if b.Err() != nil {
		return false
	}
	b.SetPC(e.pc)
	op(b)
	return b.Err() == nil
}

func (e *TextAssembler) validErr(op func(*ByteAssembler) error) error {
	b := e.encoder()
	if err := b.Err(); err != nil {
		return err
	}
	b.SetPC(e.pc)
	if err := op(b); err != nil {
		return err
	}
	return b.Err()
}

func (e *TextAssembler) AssembledSize() int { return e.pc }
func (e *TextAssembler) Text() string       { return strings.Join(e.lines, "\n") + "\n" }
func (e *TextAssembler) line(size int, format string, args ...any) {
	e.lines = append(e.lines, fmt.Sprintf("%04x  %s", e.pc, fmt.Sprintf(format, args...)))
	e.pc += size
}
func (e *TextAssembler) Constant(dst backend.Register, value uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.Constant(dst, value) }) {
		return
	}
	name := amd64Name(dst)
	if value <= 0xffffffff {
		name = amd64RegisterNames32[dst]
	}
	e.line(ConstantSize(dst, value), "mov %s, 0x%x", name, value)
}
func (e *TextAssembler) Symbol(dst backend.Register, name string) {
	if !e.valid(func(enc *ByteAssembler) { enc.Symbol(dst, name) }) {
		return
	}
	e.line(10, "movabs %s, <%s>", amd64Name(dst), name)
}
func (e *TextAssembler) Move(dst, src backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Move(dst, src) }) {
		return
	}
	if dst != src {
		e.line(3, "mov %s, %s", amd64Name(dst), amd64Name(src))
	}
}
func (e *TextAssembler) Binary(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Binary(operation, width, dst, left, right) }) {
		return
	}
	e.Move(dst, left)
	mnemonic := map[op.Opcode]string{op.OpAdd: "add", op.OpSub: "sub", op.OpMul: "imul", op.OpAnd: "and", op.OpOr: "or", op.OpXor: "xor"}[operation]
	size := 3
	name := amd64Name
	if width == 8 && operation == op.OpMul {
		width = 64
	}
	switch width {
	case 8:
		name = func(reg backend.Register) string { return amd64RegisterNames8[reg] }
	case 16:
		name = func(reg backend.Register) string { return amd64RegisterNames16[reg] }
		size++
	case 32:
		name = func(reg backend.Register) string { return amd64RegisterNames32[reg] }
	}
	if operation == op.OpMul {
		size = 4
		if width == 16 {
			size = 5
		}
	}
	e.line(size, "%s %s, %s", mnemonic, name(dst), name(right))
}
func (e *TextAssembler) BinaryImmediate(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.BinaryImmediate(operation, width, dst, left, value) }) {
		return
	}
	e.Move(dst, left)
	mnemonic := map[op.Opcode]string{op.OpAdd: "add", op.OpSub: "sub", op.OpAnd: "and", op.OpOr: "or", op.OpXor: "xor"}[operation]
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
func (e *TextAssembler) Decrement(dst, src backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Decrement(dst, src) }) {
		return
	}
	e.Move(dst, src)
	e.line(3, "dec %s", amd64Name(dst))
}
func (e *TextAssembler) Increment(dst, src backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Increment(dst, src) }) {
		return
	}
	e.Move(dst, src)
	e.line(3, "inc %s", amd64Name(dst))
}
func (e *TextAssembler) Narrow(dst, src backend.Register, width uint8) {
	if !e.valid(func(enc *ByteAssembler) { enc.Narrow(dst, src, width) }) {
		return
	}
	switch width {
	case 64:
		e.Move(dst, src)
	case 8:
		e.line(4, "movzx %s, %s", amd64RegisterNames32[dst], amd64RegisterNames8[src])
	case 16:
		e.line(4, "movzx %s, %s", amd64RegisterNames32[dst], amd64RegisterNames16[src])
	case 32:
		e.line(3, "mov %s, %s", amd64RegisterNames32[dst], amd64RegisterNames32[src])
	}
}
func (e *TextAssembler) SignExtend(dst, src backend.Register, width uint8) {
	if !e.valid(func(enc *ByteAssembler) { enc.SignExtend(dst, src, width) }) {
		return
	}
	switch width {
	case 64:
		e.Move(dst, src)
	case 8:
		e.line(4, "movsx %s, %s", amd64Name(dst), amd64RegisterNames8[src])
	case 16:
		e.line(4, "movsx %s, %s", amd64Name(dst), amd64RegisterNames16[src])
	case 32:
		e.line(3, "movsxd %s, %s", amd64Name(dst), amd64RegisterNames32[src])
	}
}
func (e *TextAssembler) Shift(operation op.Opcode, width uint8, dst, src backend.Register, count uint8) {
	if !e.valid(func(enc *ByteAssembler) { enc.Shift(operation, width, dst, src, count) }) {
		return
	}
	if code := rorxPlan(operation, width, dst, src, count, e.profile); code != nil {
		name := amd64Name
		if width == 32 {
			name = func(r backend.Register) string { return amd64RegisterNames32[r] }
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
	mnemonic := map[op.Opcode]string{op.OpShiftLeft: "shl", op.OpShiftRight: "shr", op.OpShiftRightArithmetic: "sar", op.OpRotateLeft: "rol", op.OpRotateRight: "ror"}[operation]
	size := 4
	if width == 16 {
		size++
	}
	e.line(size, "%s %s, %d", mnemonic, name, count)
}
func (e *TextAssembler) Scaled(dst, base, index backend.Register, scale, disp uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.Scaled(dst, base, index, scale, disp) }) {
		return
	}
	e.line(IndexedSize(base, disp), "lea %s, %s", amd64Name(dst), amd64Address(base, index, scale, disp, true))
}
func (e *TextAssembler) SelectValue(dst, condition, ifTrue, ifFalse backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.SelectValue(dst, condition, ifTrue, ifFalse) }) {
		return
	}
	e.Move(dst, ifFalse)
	e.line(3, "test %s, %s", amd64Name(condition), amd64Name(condition))
	e.line(4, "cmovne %s, %s", amd64Name(dst), amd64Name(ifTrue))
}
func (e *TextAssembler) Load64(dst, address backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Load64(dst, address) }) {
		return
	}
	e.line(MemorySize(address), "mov %s, qword ptr [%s]", amd64Name(dst), amd64Name(address))
}
func (e *TextAssembler) Store64(address, value backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Store64(address, value) }) {
		return
	}
	e.line(MemorySize(address), "mov qword ptr [%s], %s", amd64Name(address), amd64Name(value))
}
func (e *TextAssembler) Load8(dst, address backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Load8(dst, address) }) {
		return
	}
	e.line(MemorySize(address)+1, "movzx %s, byte ptr [%s]", amd64Name(dst), amd64Name(address))
}
func (e *TextAssembler) Store8(address, value backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Store8(address, value) }) {
		return
	}
	e.line(MemorySize(address), "mov byte ptr [%s], %s", amd64Name(address), amd64RegisterNames8[value])
}
func (e *TextAssembler) LoadAddress(dst, base, index backend.Register, scale, disp uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.LoadAddress(dst, base, index, scale, disp) }) {
		return
	}
	e.line(IndexedSize(base, disp), "mov %s, qword ptr %s", amd64Name(dst), amd64Address(base, index, scale, disp, true))
}
func (e *TextAssembler) StoreAddress(base, index, value backend.Register, scale, disp uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.StoreAddress(base, index, value, scale, disp) }) {
		return
	}
	e.line(IndexedSize(base, disp), "mov qword ptr %s, %s", amd64Address(base, index, scale, disp, true), amd64Name(value))
}
func (e *TextAssembler) LoadOffset(dst, base backend.Register, disp uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.LoadOffset(dst, base, disp) }) {
		return
	}
	e.line(OffsetSize(base, disp), "mov %s, qword ptr %s", amd64Name(dst), amd64Address(base, 0, 0, disp, false))
}
func (e *TextAssembler) StoreOffset(base, value backend.Register, disp uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.StoreOffset(base, value, disp) }) {
		return
	}
	e.line(OffsetSize(base, disp), "mov qword ptr %s, %s", amd64Address(base, 0, 0, disp, false), amd64Name(value))
}
func (e *TextAssembler) Syscall() {
	if !e.valid(func(enc *ByteAssembler) { enc.Syscall() }) {
		return
	}
	e.line(2, "syscall")
}
func (e *TextAssembler) CallC() {
	if !e.valid(func(enc *ByteAssembler) { enc.CallC() }) {
		return
	}
	e.line(2, "xor EAX, EAX")
	e.line(3, "call R11")
}
func (e *TextAssembler) CallGo() {
	if !e.valid(func(enc *ByteAssembler) { enc.CallGo() }) {
		return
	}
	e.line(3, "call R11")
}
func (*TextAssembler) SaveLink(int)    {}
func (*TextAssembler) RestoreLink(int) {}
func (e *TextAssembler) Spill(src backend.Register, slot int) {
	if !e.valid(func(enc *ByteAssembler) { enc.Spill(src, slot) }) {
		return
	}
	base := backend.Register(10)
	size := 7
	if e.stack == abi.StackPlatform {
		base = 4
		size = 8
		slot += e.goHomes
	}
	e.line(size, "mov qword ptr [%s + %d], %s", amd64Name(base), slot*8, amd64Name(src))
}
func (e *TextAssembler) Reload(dst backend.Register, slot int) {
	if !e.valid(func(enc *ByteAssembler) { enc.Reload(dst, slot) }) {
		return
	}
	base := backend.Register(10)
	size := 7
	if e.stack == abi.StackPlatform {
		base = 4
		size = 8
		slot += e.goHomes
	}
	e.line(size, "mov %s, qword ptr [%s + %d]", amd64Name(dst), amd64Name(base), slot*8)
}
func (e *TextAssembler) FrameEnter(frame int) {
	if !e.valid(func(enc *ByteAssembler) { enc.FrameEnter(frame) }) {
		return
	}
	e.line(7, "sub RSP, %d", frame)
}
func (e *TextAssembler) FrameLeave(frame int) {
	if !e.valid(func(enc *ByteAssembler) { enc.FrameLeave(frame) }) {
		return
	}
	e.line(7, "add RSP, %d", frame)
}
func (e *TextAssembler) Ret() {
	if !e.valid(func(enc *ByteAssembler) { enc.Ret() }) {
		return
	}
	e.line(1, "ret")
}
func (e *TextAssembler) CompareImmediate(reg backend.Register, value int64) {
	if !e.valid(func(enc *ByteAssembler) { enc.CompareImmediate(reg, value) }) {
		return
	}
	e.line(7, "cmp %s, %d", amd64Name(reg), value)
}
func (e *TextAssembler) CompareRegister(left, right backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.CompareRegister(left, right) }) {
		return
	}
	e.line(3, "cmp %s, %s", amd64Name(left), amd64Name(right))
}
func (e *TextAssembler) Jump(target int) {
	if !e.valid(func(enc *ByteAssembler) { enc.Jump(target) }) {
		return
	}
	e.line(5, "jmp 0x%x", target)
}
func (e *TextAssembler) JumpIf(condition op.BranchCondition, target int) {
	if !e.valid(func(enc *ByteAssembler) { enc.JumpIf(condition, target) }) {
		return
	}
	mnemonic := [...]string{"je", "jnz", "jl", "jge", "jle", "jg", "jb", "jae", "jbe", "ja", "js", "jns"}[condition]
	e.line(6, "%s 0x%x", mnemonic, target)
}

func (e *TextAssembler) BranchZero(reg backend.Register, width uint8, nonzero bool, target int, long bool) {
	if !e.valid(func(enc *ByteAssembler) { enc.BranchZero(reg, width, nonzero, target, long) }) {
		return
	}
	if !long {
		e.line(2, "jrcxz 0x%x", target)
		return
	}
	e.line(2, "jrcxz 0x%x", e.pc+7)
	e.line(5, "jmp 0x%x", e.pc+10)
	e.line(5, "jmp 0x%x", target)
}

func (e *TextAssembler) Loop(target int, long bool) {
	if !e.valid(func(enc *ByteAssembler) { enc.Loop(target, long) }) {
		return
	}
	if !long {
		e.line(2, "loop 0x%x", target)
		return
	}
	e.line(4, "lea RCX, [RCX - 1]")
	e.line(2, "jrcxz 0x%x", e.pc+7)
	e.line(5, "jmp 0x%x", target)
}

// WithCPU selects an immutable contract for amd64 (or the zero baseline).
// It panics if p was resolved for another architecture.
func (e *TextAssembler) WithCPU(p abi.ResolvedCPU) *TextAssembler {
	if p.Architecture() != "" && p.Architecture() != "amd64" {
		panic("amd64: foreign CPU architecture")
	}
	e.profile = p
	if e.check != nil {
		e.check.WithCPU(p)
	}
	return e
}
