package arm64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"math/bits"
	"strings"
)

func NewTextAssembler(stack abi.StackPractice, goHomes int) *TextAssembler {
	return &TextAssembler{stack: stack, goHomes: goHomes}
}

// TextAssembler visits physical instructions without encoding any words.
type TextAssembler struct {
	lines   []string
	pc      int
	stack   abi.StackPractice
	goHomes int
}

func (e *TextAssembler) SetPC(pc int)       { e.pc = pc }
func (e *TextAssembler) AssembledSize() int { return e.pc }
func (e *TextAssembler) Text() string       { return strings.Join(e.lines, "\n") + "\n" }
func (e *TextAssembler) line(format string, args ...any) {
	e.lines = append(e.lines, fmt.Sprintf("%04x  %s", e.pc, fmt.Sprintf(format, args...)))
	e.pc += 4
}
func (e *TextAssembler) Constant(dst machine.Register, value uint64) {
	for _, part := range ImmediatePlan(value) {
		if part.Shift == 0 {
			e.line("%s X%d, #0x%x", part.Op, dst, part.Imm)
		} else {
			e.line("%s X%d, #0x%x, lsl #%d", part.Op, dst, part.Imm, part.Shift)
		}
	}
}
func (e *TextAssembler) Symbol(dst machine.Register, name string) {
	e.line("movz X%d, #<%s>[0:16]", dst, name)
	for i := 1; i < 4; i++ {
		e.line("movk X%d, #<%s>[%d:%d], lsl #%d", dst, name, i*16, (i+1)*16, i*16)
	}
}
func (e *TextAssembler) Move(dst, src machine.Register) {
	if dst != src {
		e.line("mov X%d, X%d", dst, src)
	}
}
func (e *TextAssembler) Binary(operation ir.Opcode, width uint8, dst, left, right machine.Register) {
	mnemonic := map[ir.Opcode]string{ir.OpAdd: "adds", ir.OpSub: "subs", ir.OpMul: "mul", ir.OpAnd: "ands", ir.OpOr: "orr", ir.OpXor: "eor"}[operation]
	register := "X"
	if width != 0 && width < 64 {
		register = "W"
	}
	e.line("%s %s%d, %s%d, %s%d", mnemonic, register, dst, register, left, register, right)
}
func (e *TextAssembler) BinaryImmediate(operation ir.Opcode, width uint8, dst, left machine.Register, value uint64) {
	if operation == ir.OpAnd || operation == ir.OpOr || operation == ir.OpXor {
		_, dataWidth, ok := logicalImmediatePlan(width, value)
		if !ok {
			panic("arm64: invalid logical immediate")
		}
		register := "X"
		if dataWidth == 32 {
			register = "W"
			value = uint64(uint32(value))
		}
		mnemonic := map[ir.Opcode]string{ir.OpAnd: "ands", ir.OpOr: "orr", ir.OpXor: "eor"}[operation]
		e.line("%s %s%d, %s%d, #0x%x", mnemonic, register, dst, register, left, value)
		return
	}
	effective, immediate, shift, ok := addSubImmediate(operation, width, value)
	if !ok {
		panic("arm64: invalid arithmetic immediate")
	}
	mnemonic := "adds"
	if effective == ir.OpSub {
		mnemonic = "subs"
	}
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
func (e *TextAssembler) Decrement(dst, src machine.Register) {
	e.line("subs X%d, X%d, #1", dst, src)
}
func (e *TextAssembler) Narrow(dst, src machine.Register, width uint8) {
	e.line("ubfm X%d, X%d, #0, #%d", dst, src, width-1)
}
func (e *TextAssembler) SignExtend(dst, src machine.Register, width uint8) {
	e.line("sbfm X%d, X%d, #0, #%d", dst, src, width-1)
}
func (e *TextAssembler) Shift(operation ir.Opcode, width uint8, dst, src machine.Register, count uint8) {
	register := "X"
	if width < 64 && operation != ir.OpShiftRightArithmetic || width == 32 {
		register = "W"
	}
	if operation == ir.OpShiftRightArithmetic && width < 32 {
		e.line("sbfm X%d, X%d, #%d, #%d", dst, src, count, width-1)
		return
	}
	mnemonic := map[ir.Opcode]string{ir.OpShiftLeft: "lsl", ir.OpShiftRight: "lsr", ir.OpShiftRightArithmetic: "asr", ir.OpRotateLeft: "ror", ir.OpRotateRight: "ror"}[operation]
	if operation == ir.OpRotateLeft {
		count = (width - count) % width
	}
	e.line("%s %s%d, %s%d, #%d", mnemonic, register, dst, register, src, count)
}
func (e *TextAssembler) Scaled(dst, base, index machine.Register, scale, _ uint64) {
	e.line("add X%d, X%d, X%d, lsl #%d", dst, base, index, bits.TrailingZeros64(scale))
}

func (e *TextAssembler) MulAdd(dst, base, index, factor machine.Register) {
	e.line("madd X%d, X%d, X%d, X%d", dst, index, factor, base)
}
func (e *TextAssembler) SelectValue(dst, condition, ifTrue, ifFalse machine.Register) {
	e.line("cmp X%d, #0", condition)
	e.line("csel X%d, X%d, X%d, ne", dst, ifTrue, ifFalse)
}
func (e *TextAssembler) SelectFlags(dst, ifTrue, ifFalse machine.Register, condition ir.BranchCondition) {
	mnemonic := [...]string{"eq", "ne", "lt", "ge", "le", "gt", "lo", "hs", "ls", "hi", "mi", "pl"}[condition]
	e.line("csel X%d, X%d, X%d, %s", dst, ifTrue, ifFalse, mnemonic)
}
func (e *TextAssembler) Load64(dst, address machine.Register) {
	e.line("ldr X%d, [X%d]", dst, address)
}
func (e *TextAssembler) Store64(address, value machine.Register) {
	e.line("str X%d, [X%d]", value, address)
}
func (e *TextAssembler) Load8(dst, address machine.Register) {
	e.line("ldrb W%d, [X%d]", dst, address)
}
func (e *TextAssembler) Store8(address, value machine.Register) {
	e.line("strb W%d, [X%d]", value, address)
}
func (e *TextAssembler) LoadAddress(dst, base, index machine.Register, scale, _ uint64) {
	if scale == 8 {
		e.line("ldr X%d, [X%d, X%d, lsl #3]", dst, base, index)
	} else {
		e.line("ldr X%d, [X%d, X%d]", dst, base, index)
	}
}
func (e *TextAssembler) StoreAddress(base, index, value machine.Register, scale, _ uint64) {
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
func (e *TextAssembler) LoadOffset(dst, base machine.Register, disp uint64) {
	if arm64ScaledOffset(disp) {
		e.line("ldr X%d, [X%d, #%d]", dst, base, disp)
	} else {
		e.line("ldur X%d, [X%d, #%d]", dst, base, int64(disp))
	}
}
func (e *TextAssembler) StoreOffset(base, value machine.Register, disp uint64) {
	if arm64ScaledOffset(disp) {
		e.line("str X%d, [X%d, #%d]", value, base, disp)
	} else {
		e.line("stur X%d, [X%d, #%d]", value, base, int64(disp))
	}
}
func (e *TextAssembler) Syscall() {
	e.line("svc #0")
}
func (e *TextAssembler) CallC() {
	e.line("blr X16")
}
func (e *TextAssembler) CallGo() { e.line("blr X16") }
func (e *TextAssembler) Spill(src machine.Register, slot int) {
	base := "X8"
	if e.stack == abi.StackPlatform {
		base = "SP"
		slot += 1 + e.goHomes
	}
	e.line("str X%d, [%s, #%d]", src, base, slot*8)
}
func (e *TextAssembler) Reload(dst machine.Register, slot int) {
	base := "X8"
	if e.stack == abi.StackPlatform {
		base = "SP"
		slot += 1 + e.goHomes
	}
	e.line("ldr X%d, [%s, #%d]", dst, base, slot*8)
}

func (e *TextAssembler) SaveCFloat(reg machine.Register, slot int) {
	e.line("str D%d, [SP, #%d]", reg, (slot+1+e.goHomes)*8)
}

func (e *TextAssembler) RestoreCFloat(reg machine.Register, slot int) {
	e.line("ldr D%d, [SP, #%d]", reg, (slot+1+e.goHomes)*8)
}
func (e *TextAssembler) SaveLink(slot int) {
	if e.stack == abi.StackPlatform {
		e.line("str X30, [SP]")
		return
	}
	e.Spill(30, slot)
}
func (e *TextAssembler) RestoreLink(slot int) {
	if e.stack == abi.StackPlatform {
		e.line("ldr X30, [SP]")
		return
	}
	e.Reload(30, slot)
}
func (e *TextAssembler) FrameEnter(frame int) {
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
func (e *TextAssembler) Ret() { e.line("ret") }
func (e *TextAssembler) CompareImmediate(reg machine.Register, value int64) {
	if value < 0 {
		e.line("cmn X%d, #%d", reg, -value)
	} else {
		e.line("cmp X%d, #%d", reg, value)
	}
}
func (e *TextAssembler) CompareRegister(left, right machine.Register) {
	e.line("cmp X%d, X%d", left, right)
}
func (e *TextAssembler) Jump(target int) { e.line("b 0x%x", target) }
func (e *TextAssembler) JumpIf(condition ir.BranchCondition, target int) {
	mnemonic := [...]string{"eq", "ne", "lt", "ge", "le", "gt", "lo", "hs", "ls", "hi", "mi", "pl"}[condition]
	e.line("b.%s 0x%x", mnemonic, target)
}

func (e *TextAssembler) BranchZero(reg machine.Register, width uint8, nonzero bool, target int, long bool) {
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

func (e *TextAssembler) BranchBit(reg machine.Register, _ uint8, bit uint8, nonzero bool, target int, long bool) {
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

func (e *TextAssembler) DecrementNoFlags(d, s machine.Register) { e.line("sub X%d, X%d, #1", d, s) }
