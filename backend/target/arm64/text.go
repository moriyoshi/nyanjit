package arm64

import (
	"fmt"
	"math/bits"
	"strings"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func NewTextAssembler(stack abi.StackPractice, goHomes int) *TextAssembler {
	return &TextAssembler{stack: stack, goHomes: goHomes}
}

// TextAssembler renders physical instructions as text. A private byte encoder
// checks every operation first, and Err reports its rejection.
type TextAssembler struct {
	lines   []string
	pc      int
	stack   abi.StackPractice
	goHomes int
	check   *ByteAssembler
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
		e.check = NewByteAssembler(e.stack, e.goHomes)
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
func (e *TextAssembler) line(format string, args ...any) {
	e.lines = append(e.lines, fmt.Sprintf("%04x  %s", e.pc, fmt.Sprintf(format, args...)))
	e.pc += 4
}
func (e *TextAssembler) Constant(dst backend.Register, value uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.Constant(dst, value) }) {
		return
	}
	parts := immediatePlan(value)
	for _, part := range parts.parts[:parts.n] {
		if part.shift == 0 {
			e.line("%s X%d, #0x%x", part.op, dst, part.imm)
		} else {
			e.line("%s X%d, #0x%x, lsl #%d", part.op, dst, part.imm, part.shift)
		}
	}
}
func (e *TextAssembler) Symbol(dst backend.Register, name string) {
	if !e.valid(func(enc *ByteAssembler) { enc.Symbol(dst, name) }) {
		return
	}
	e.line("movz X%d, #<%s>[0:16]", dst, name)
	for i := 1; i < 4; i++ {
		e.line("movk X%d, #<%s>[%d:%d], lsl #%d", dst, name, i*16, (i+1)*16, i*16)
	}
}
func (e *TextAssembler) Move(dst, src backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Move(dst, src) }) {
		return
	}
	if dst != src {
		e.line("mov X%d, X%d", dst, src)
	}
}
func (e *TextAssembler) Binary(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Binary(operation, width, dst, left, right) }) {
		return
	}
	e.binary(operation, width, dst, left, right, true)
}

func (e *TextAssembler) BinaryNoFlags(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.BinaryNoFlags(operation, width, dst, left, right) }) {
		return
	}
	e.binary(operation, width, dst, left, right, false)
}

func (e *TextAssembler) binary(operation op.Opcode, width uint8, dst, left, right backend.Register, setFlags bool) {
	register := "X"
	if width != 0 && width < 64 {
		register = "W"
	}
	e.line("%s %s%d, %s%d, %s%d", binaryMnemonic(operation, setFlags), register, dst, register, left, register, right)
}

// binaryMnemonic names the register or logical-immediate form of operation.
func binaryMnemonic(operation op.Opcode, setFlags bool) string {
	switch operation {
	case op.OpAdd:
		if setFlags {
			return "adds"
		}
		return "add"
	case op.OpSub:
		if setFlags {
			return "subs"
		}
		return "sub"
	case op.OpAnd:
		if setFlags {
			return "ands"
		}
		return "and"
	case op.OpMul:
		return "mul"
	case op.OpOr:
		return "orr"
	case op.OpXor:
		return "eor"
	}
	return ""
}

func (e *TextAssembler) BinaryImmediate(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.BinaryImmediate(operation, width, dst, left, value) }) {
		return
	}
	e.binaryImmediate(operation, width, dst, left, value, true)
}

func (e *TextAssembler) BinaryImmediateNoFlags(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.BinaryImmediateNoFlags(operation, width, dst, left, value) }) {
		return
	}
	e.binaryImmediate(operation, width, dst, left, value, false)
}

func (e *TextAssembler) binaryImmediate(operation op.Opcode, width uint8, dst, left backend.Register, value uint64, setFlags bool) {
	if operation == op.OpAnd || operation == op.OpOr || operation == op.OpXor {
		_, dataWidth, ok := logicalImmediatePlan(width, value)
		if !ok {
			panic("arm64: invalid logical immediate")
		}
		register := "X"
		if dataWidth == 32 {
			register = "W"
			value = uint64(uint32(value))
		}
		e.line("%s %s%d, %s%d, #0x%x", binaryMnemonic(operation, setFlags), register, dst, register, left, value)
		return
	}
	effective, immediate, shift, ok := addSubImmediate(operation, width, value)
	if !ok {
		panic("arm64: invalid arithmetic immediate")
	}
	mnemonic := binaryMnemonic(effective, setFlags)
	register := "X"
	if width < 64 {
		register = "W"
	}
	if shift == 0 {
		e.line("%s %s%d, %s%d, #%d", mnemonic, register, dst, register, left, immediate)
	} else {
		e.line("%s %s%d, %s%d, #%d, lsl #12", mnemonic, register, dst, register, left, immediate)
	}
}
func (e *TextAssembler) Decrement(dst, src backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Decrement(dst, src) }) {
		return
	}
	e.line("subs X%d, X%d, #1", dst, src)
}
func (e *TextAssembler) Narrow(dst, src backend.Register, width uint8) {
	if !e.valid(func(enc *ByteAssembler) { enc.Narrow(dst, src, width) }) {
		return
	}
	e.line("ubfm X%d, X%d, #0, #%d", dst, src, width-1)
}
func (e *TextAssembler) SignExtend(dst, src backend.Register, width uint8) {
	if !e.valid(func(enc *ByteAssembler) { enc.SignExtend(dst, src, width) }) {
		return
	}
	e.line("sbfm X%d, X%d, #0, #%d", dst, src, width-1)
}
func (e *TextAssembler) Shift(operation op.Opcode, width uint8, dst, src backend.Register, count uint8) {
	if !e.valid(func(enc *ByteAssembler) { enc.Shift(operation, width, dst, src, count) }) {
		return
	}
	register := "X"
	if width < 64 && operation != op.OpShiftRightArithmetic || width == 32 {
		register = "W"
	}
	if operation == op.OpShiftRightArithmetic && width < 32 {
		e.line("sbfm X%d, X%d, #%d, #%d", dst, src, count, width-1)
		return
	}
	mnemonic := map[op.Opcode]string{op.OpShiftLeft: "lsl", op.OpShiftRight: "lsr", op.OpShiftRightArithmetic: "asr", op.OpRotateLeft: "ror", op.OpRotateRight: "ror"}[operation]
	if operation == op.OpRotateLeft {
		count = (width - count) % width
	}
	e.line("%s %s%d, %s%d, #%d", mnemonic, register, dst, register, src, count)
}
func (e *TextAssembler) Scaled(dst, base, index backend.Register, scale, disp uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.Scaled(dst, base, index, scale, disp) }) {
		return
	}
	e.line("add X%d, X%d, X%d, lsl #%d", dst, base, index, bits.TrailingZeros64(scale))
}

func (e *TextAssembler) MulAdd(dst, base, index, factor backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.MulAdd(dst, base, index, factor) }) {
		return
	}
	e.line("madd X%d, X%d, X%d, X%d", dst, index, factor, base)
}
func (e *TextAssembler) SelectValue(dst, condition, ifTrue, ifFalse backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.SelectValue(dst, condition, ifTrue, ifFalse) }) {
		return
	}
	e.line("cmp X%d, #0", condition)
	e.line("csel X%d, X%d, X%d, ne", dst, ifTrue, ifFalse)
}
func (e *TextAssembler) SelectFlags(dst, ifTrue, ifFalse backend.Register, condition op.BranchCondition) {
	if !e.valid(func(enc *ByteAssembler) { enc.SelectFlags(dst, ifTrue, ifFalse, condition) }) {
		return
	}
	mnemonic := [...]string{"eq", "ne", "lt", "ge", "le", "gt", "lo", "hs", "ls", "hi", "mi", "pl"}[condition]
	e.line("csel X%d, X%d, X%d, %s", dst, ifTrue, ifFalse, mnemonic)
}
func (e *TextAssembler) Load64(dst, address backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Load64(dst, address) }) {
		return
	}
	e.line("ldr X%d, [X%d]", dst, address)
}
func (e *TextAssembler) Store64(address, value backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Store64(address, value) }) {
		return
	}
	e.line("str X%d, [X%d]", value, address)
}
func (e *TextAssembler) Load8(dst, address backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Load8(dst, address) }) {
		return
	}
	e.line("ldrb W%d, [X%d]", dst, address)
}
func (e *TextAssembler) Store8(address, value backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.Store8(address, value) }) {
		return
	}
	e.line("strb W%d, [X%d]", value, address)
}
func (e *TextAssembler) LoadAddress(dst, base, index backend.Register, scale, disp uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.LoadAddress(dst, base, index, scale, disp) }) {
		return
	}
	if scale == 8 {
		e.line("ldr X%d, [X%d, X%d, lsl #3]", dst, base, index)
	} else {
		e.line("ldr X%d, [X%d, X%d]", dst, base, index)
	}
}
func (e *TextAssembler) StoreAddress(base, index, value backend.Register, scale, disp uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.StoreAddress(base, index, value, scale, disp) }) {
		return
	}
	if scale == 8 {
		e.line("str X%d, [X%d, X%d, lsl #3]", value, base, index)
	} else {
		e.line("str X%d, [X%d, X%d]", value, base, index)
	}
}
func arm64ScaledOffset(disp uint64) bool {
	offset := int64(disp)
	return offset >= 0 && offset <= 4095*8 && offset%8 == 0
}
func (e *TextAssembler) LoadOffset(dst, base backend.Register, disp uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.LoadOffset(dst, base, disp) }) {
		return
	}
	if arm64ScaledOffset(disp) {
		e.line("ldr X%d, [X%d, #%d]", dst, base, disp)
	} else {
		e.line("ldur X%d, [X%d, #%d]", dst, base, int64(disp))
	}
}
func (e *TextAssembler) StoreOffset(base, value backend.Register, disp uint64) {
	if !e.valid(func(enc *ByteAssembler) { enc.StoreOffset(base, value, disp) }) {
		return
	}
	if arm64ScaledOffset(disp) {
		e.line("str X%d, [X%d, #%d]", value, base, disp)
	} else {
		e.line("stur X%d, [X%d, #%d]", value, base, int64(disp))
	}
}
func (e *TextAssembler) Syscall() {
	if !e.valid(func(enc *ByteAssembler) { enc.Syscall() }) {
		return
	}
	e.line("svc #0")
}
func (e *TextAssembler) CallC() {
	if !e.valid(func(enc *ByteAssembler) { enc.CallC() }) {
		return
	}
	e.line("blr X16")
}
func (e *TextAssembler) CallGo() {
	if !e.valid(func(enc *ByteAssembler) { enc.CallGo() }) {
		return
	}
	e.line("blr X16")
}
func (e *TextAssembler) Spill(src backend.Register, slot int) {
	if !e.valid(func(enc *ByteAssembler) { enc.Spill(src, slot) }) {
		return
	}
	base := "X8"
	if e.stack == abi.StackPlatform {
		base = "SP"
		slot += 1 + e.goHomes
	}
	e.line("str X%d, [%s, #%d]", src, base, slot*8)
}
func (e *TextAssembler) Reload(dst backend.Register, slot int) {
	if !e.valid(func(enc *ByteAssembler) { enc.Reload(dst, slot) }) {
		return
	}
	base := "X8"
	if e.stack == abi.StackPlatform {
		base = "SP"
		slot += 1 + e.goHomes
	}
	e.line("ldr X%d, [%s, #%d]", dst, base, slot*8)
}

func (e *TextAssembler) SaveCFloat(reg backend.Register, slot int) {
	if !e.valid(func(enc *ByteAssembler) { enc.SaveCFloat(reg, slot) }) {
		return
	}
	e.line("str D%d, [SP, #%d]", reg, (slot+1+e.goHomes)*8)
}

func (e *TextAssembler) RestoreCFloat(reg backend.Register, slot int) {
	if !e.valid(func(enc *ByteAssembler) { enc.RestoreCFloat(reg, slot) }) {
		return
	}
	e.line("ldr D%d, [SP, #%d]", reg, (slot+1+e.goHomes)*8)
}
func (e *TextAssembler) SaveLink(slot int) {
	if !e.valid(func(enc *ByteAssembler) { enc.SaveLink(slot) }) {
		return
	}
	if e.stack == abi.StackPlatform {
		e.line("str X30, [SP]")
		return
	}
	e.Spill(30, slot)
}
func (e *TextAssembler) RestoreLink(slot int) {
	if !e.valid(func(enc *ByteAssembler) { enc.RestoreLink(slot) }) {
		return
	}
	if e.stack == abi.StackPlatform {
		e.line("ldr X30, [SP]")
		return
	}
	e.Reload(30, slot)
}
func (e *TextAssembler) FrameEnter(frame int) {
	if !e.valid(func(enc *ByteAssembler) { enc.FrameEnter(frame) }) {
		return
	}
	if frame <= 240 {
		e.line("str X30, [SP, #-%d]!", frame)
		e.line("stur X29, [SP, #-8]")
		e.line("sub X29, SP, #8")
		return
	}
	e.line("mov X17, SP")
	for amount := frame; amount > 0; {
		step := amount
		if step > 4080 {
			step = 4080
		}
		e.line("sub X17, X17, #%d", step)
		amount -= step
	}
	e.line("str X30, [X17]")
	e.line("stur X29, [X17, #-8]")
	e.line("mov SP, X17")
	e.line("sub X29, SP, #8")
}
func (e *TextAssembler) FrameLeave(frame int) {
	if !e.valid(func(enc *ByteAssembler) { enc.FrameLeave(frame) }) {
		return
	}
	e.line("ldur X29, [SP, #-8]")
	if frame <= 240 {
		e.line("ldr X30, [SP], #%d", frame)
		return
	}
	e.line("ldr X30, [SP]")
	e.adjustSP(frame, "add")
}
func (e *TextAssembler) adjustSP(frame int, mnemonic string) {
	for amount := frame; amount > 0; {
		step := amount
		if step > 4080 {
			step = 4080
		}
		e.line("%s SP, SP, #%d", mnemonic, step)
		amount -= step
	}
}
func (e *TextAssembler) Ret() {
	if !e.valid(func(enc *ByteAssembler) { enc.Ret() }) {
		return
	}
	e.line("ret")
}
func (e *TextAssembler) CompareImmediate(reg backend.Register, value int64) {
	if !e.valid(func(enc *ByteAssembler) { enc.CompareImmediate(reg, value) }) {
		return
	}
	if value < 0 {
		e.line("cmn X%d, #%d", reg, -value)
	} else {
		e.line("cmp X%d, #%d", reg, value)
	}
}
func (e *TextAssembler) CompareRegister(left, right backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.CompareRegister(left, right) }) {
		return
	}
	e.line("cmp X%d, X%d", left, right)
}
func (e *TextAssembler) Jump(target int) {
	if !e.valid(func(enc *ByteAssembler) { enc.Jump(target) }) {
		return
	}
	e.line("b 0x%x", target)
}
func (e *TextAssembler) JumpIf(condition op.BranchCondition, target int) {
	if !e.valid(func(enc *ByteAssembler) { enc.JumpIf(condition, target) }) {
		return
	}
	mnemonic := [...]string{"eq", "ne", "lt", "ge", "le", "gt", "lo", "hs", "ls", "hi", "mi", "pl"}[condition]
	e.line("b.%s 0x%x", mnemonic, target)
}

func (e *TextAssembler) JumpIfForm(condition op.BranchCondition, target int, long bool) {
	if !e.valid(func(enc *ByteAssembler) { enc.JumpIfForm(condition, target, long) }) {
		return
	}
	if !long {
		e.JumpIf(condition, target)
		return
	}
	e.JumpIf(condition^1, e.pc+8)
	e.Jump(target)
}

func (e *TextAssembler) BranchZero(reg backend.Register, width uint8, nonzero bool, target int, long bool) {
	if !e.valid(func(enc *ByteAssembler) { enc.BranchZero(reg, width, nonzero, target, long) }) {
		return
	}
	name := "cbz"
	if nonzero {
		name = "cbnz"
	}
	view := "X"
	if width == 32 {
		view = "W"
	}
	if long {
		if nonzero {
			name = "cbz"
		} else {
			name = "cbnz"
		}
		e.line("%s %s%d, 0x%x", name, view, reg, e.pc+8)
		e.line("b 0x%x", target)
		return
	}
	e.line("%s %s%d, 0x%x", name, view, reg, target)
}

func (e *TextAssembler) BranchBit(reg backend.Register, width uint8, bit uint8, nonzero bool, target int, long bool) {
	if !e.valid(func(enc *ByteAssembler) { enc.BranchBit(reg, width, bit, nonzero, target, long) }) {
		return
	}
	name := "tbz"
	if nonzero {
		name = "tbnz"
	}
	view := "W"
	if bit >= 32 {
		view = "X"
	}
	if long {
		if nonzero {
			name = "tbz"
		} else {
			name = "tbnz"
		}
		e.line("%s %s%d, #%d, 0x%x", name, view, reg, bit, e.pc+8)
		e.line("b 0x%x", target)
		return
	}
	e.line("%s %s%d, #%d, 0x%x", name, view, reg, bit, target)
}

func (e *TextAssembler) DecrementNoFlags(d, s backend.Register) {
	if !e.valid(func(enc *ByteAssembler) { enc.DecrementNoFlags(d, s) }) {
		return
	}
	e.line("sub X%d, X%d, #1", d, s)
}
