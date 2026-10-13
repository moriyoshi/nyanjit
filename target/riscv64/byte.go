package riscv64

import (
	"encoding/binary"
	"math/bits"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// ByteAssembler emits the uncompressed RV64IM instruction set.
type ByteAssembler struct {
	literalData               map[int]bool
	words                     []uint32
	relocations               []machine.Relocation
	stack                     abi.StackPractice
	goHomes                   int
	pc                        int
	profile                   abi.CPUProfile
	compareLeft, compareRight machine.Register
}

func NewByteAssembler(stack abi.StackPractice, goHomes int) *ByteAssembler {
	return &ByteAssembler{stack: stack, goHomes: goHomes}
}
func (e *ByteAssembler) SetPC(pc int)       { e.pc = pc }
func (e *ByteAssembler) AssembledSize() int { return len(e.words) * 4 }
func (e *ByteAssembler) Bytes() []byte {
	code := make([]byte, len(e.words)*4)
	for i, word := range e.words {
		binary.LittleEndian.PutUint32(code[i*4:], word)
	}
	return code
}
func (e *ByteAssembler) Relocations() []machine.Relocation { return e.relocations }

func rWord(funct7, rs2, rs1, funct3, rd, opcode uint32) uint32 {
	return funct7<<25 | rs2<<20 | rs1<<15 | funct3<<12 | rd<<7 | opcode
}
func iWord(imm int64, rs1, funct3, rd, opcode uint32) uint32 {
	return (uint32(imm)&0xfff)<<20 | rs1<<15 | funct3<<12 | rd<<7 | opcode
}
func sWord(imm int64, rs2, rs1, funct3 uint32) uint32 {
	u := uint32(imm) & 0xfff
	return u>>5<<25 | rs2<<20 | rs1<<15 | funct3<<12 | (u&31)<<7 | 0x23
}
func bWord(offset int, rs2, rs1, funct3 uint32) uint32 {
	u := uint32(offset) & 0x1fff
	return (u>>12&1)<<31 | (u>>5&0x3f)<<25 | rs2<<20 | rs1<<15 |
		funct3<<12 | (u>>1&15)<<8 | (u>>11&1)<<7 | 0x63
}
func jWord(offset int, rd uint32) uint32 {
	u := uint32(offset) & 0x1fffff
	return (u>>20&1)<<31 | (u>>1&0x3ff)<<21 | (u>>11&1)<<20 |
		(u>>12&0xff)<<12 | rd<<7 | 0x6f
}
func shiftWord(src, dst machine.Register, count uint8, right, arithmetic bool) uint32 {
	funct3 := uint32(1)
	if right {
		funct3 = 5
	}
	word := iWord(int64(count), uint32(src), funct3, uint32(dst), 0x13)
	if arithmetic {
		word |= 0x20 << 25
	}
	return word
}

// ConstantWords materializes any 64-bit bit pattern with RV64I instructions.
// It uses only the destination register. Symbol relocations retain their
// separate fixed-width byte sequence.
func ConstantWords(dst machine.Register, value uint64) []uint32 {
	register := uint32(dst)
	words := make([]uint32, 0, 10)
	var emit func(int64)
	emit = func(value int64) {
		if value >= -2048 && value <= 2047 {
			words = append(words, iWord(value, 0, 0, register, 0x13))
			return
		}
		if value >= -1<<31 && value <= 1<<31-1 {
			upper := (value + 2048) >> 12
			lower := value - upper<<12
			words = append(words, uint32(upper&0xfffff)<<12|register<<7|0x37)
			if lower != 0 {
				words = append(words, iWord(lower, register, 0, register, 0x1b))
			}
			return
		}
		lower := int64(uint64(value) & 0xfff)
		if lower >= 2048 {
			lower -= 4096
		}
		upper := value >> 12
		if lower < 0 {
			upper++
		}
		emit(upper)
		words = append(words, shiftWord(dst, dst, 12, false, false))
		if lower != 0 {
			words = append(words, iWord(lower, register, 0, register, 0x13))
		}
	}
	emit(int64(value))
	return words
}

func (e *ByteAssembler) Constant(dst machine.Register, value uint64) {
	e.words = append(e.words, ConstantWords(dst, value)...)
}
func (e *ByteAssembler) Symbol(dst machine.Register, name string) {
	e.relocations = append(e.relocations, machine.Relocation{Symbol: name, Offset: len(e.words) * 4, Encoding: machine.RelocRISCV64ByteSequence})
	e.words = append(e.words, iWord(0, 0, 0, uint32(dst), 0x13))
	for i := 0; i < 7; i++ {
		e.words = append(e.words, shiftWord(dst, dst, 8, false, false))
		e.words = append(e.words, iWord(0, uint32(dst), 6, uint32(dst), 0x13))
	}
}
func (e *ByteAssembler) Move(dst, src machine.Register) {
	if dst != src {
		e.words = append(e.words, iWord(0, uint32(src), 0, uint32(dst), 0x13))
	}
}
func (e *ByteAssembler) Binary(operation ir.Opcode, _ uint8, dst, left, right machine.Register) {
	funct7, funct3 := uint32(0), uint32(0)
	switch operation {
	case ir.OpSub:
		funct7 = 0x20
	case ir.OpMul:
		funct7 = 1
	case ir.OpAnd:
		funct3 = 7
	case ir.OpOr:
		funct3 = 6
	case ir.OpXor:
		funct3 = 4
	}
	e.words = append(e.words, rWord(funct7, uint32(right), uint32(left), funct3, uint32(dst), 0x33))
}
func (e *ByteAssembler) BinaryImmediate(operation ir.Opcode, _ uint8, dst, left machine.Register, value uint64) {
	immediate := int64(value)
	funct3 := uint32(0)
	switch operation {
	case ir.OpSub:
		immediate = -immediate
	case ir.OpAnd:
		funct3 = 7
	case ir.OpOr:
		funct3 = 6
	case ir.OpXor:
		funct3 = 4
	}
	e.words = append(e.words, iWord(immediate, uint32(left), funct3, uint32(dst), 0x13))
}
func (e *ByteAssembler) Decrement(dst, src machine.Register) {
	e.words = append(e.words, iWord(-1, uint32(src), 0, uint32(dst), 0x13))
}
func (e *ByteAssembler) Narrow(dst, src machine.Register, width uint8) {
	shift := 64 - width
	e.words = append(e.words, shiftWord(src, dst, shift, false, false), shiftWord(dst, dst, shift, true, false))
}
func (e *ByteAssembler) SignExtend(dst, src machine.Register, width uint8) {
	if width == 32 {
		e.words = append(e.words, iWord(0, uint32(src), 0, uint32(dst), 0x1b))
		return
	}
	shift := 64 - width
	e.words = append(e.words, shiftWord(src, dst, shift, false, false), shiftWord(dst, dst, shift, true, true))
}
func (e *ByteAssembler) Shift(operation ir.Opcode, width uint8, dst, src machine.Register, count uint8) {
	if e.profile.Has("zbb") && width >= 32 && (operation == ir.OpRotateLeft || operation == ir.OpRotateRight) {
		if operation == ir.OpRotateLeft {
			count = (width - count) % width
		}
		opcode := uint32(0x13)
		if width == 32 {
			opcode = 0x1b
		}
		e.words = append(e.words, 0x60000000|iWord(int64(count), uint32(src), 5, uint32(dst), opcode))
		return
	}
	switch operation {
	case ir.OpShiftLeft:
		e.words = append(e.words, shiftWord(src, dst, count, false, false))
	case ir.OpShiftRight:
		e.words = append(e.words, shiftWord(src, dst, count, true, false))
	case ir.OpShiftRightArithmetic:
		if width < 64 {
			e.words = append(e.words, shiftWord(src, 30, 64-width, false, false))
			e.words = append(e.words, shiftWord(30, dst, 64-width+count, true, true))
		} else {
			e.words = append(e.words, shiftWord(src, dst, count, true, true))
		}
	case ir.OpRotateLeft, ir.OpRotateRight:
		if count == 0 {
			e.Move(dst, src)
			return
		}
		left, right := count, width-count
		if operation == ir.OpRotateRight {
			left, right = width-count, count
		}
		e.words = append(e.words, shiftWord(src, 30, left, false, false), shiftWord(src, 31, right, true, false))
		e.words = append(e.words, rWord(0, 31, 30, 6, uint32(dst), 0x33))
	}
}
func (e *ByteAssembler) Scaled(dst, base, index machine.Register, scale, _ uint64) {
	if e.profile.Has("zba") && (scale == 2 || scale == 4 || scale == 8) {
		e.words = append(e.words, rWord(0x10, uint32(base), uint32(index), uint32(bits.TrailingZeros64(scale))*2, uint32(dst), 0x33))
		return
	}
	if scale == 1 {
		e.words = append(e.words, rWord(0, uint32(index), uint32(base), 0, uint32(dst), 0x33))
		return
	}
	if scale != 0 && scale&(scale-1) == 0 {
		e.words = append(e.words, shiftWord(index, 30, uint8(bits.TrailingZeros64(scale)), false, false))
	} else {
		e.words = append(e.words, ConstantWords(30, scale)...)
		e.words = append(e.words, rWord(1, 30, uint32(index), 0, 30, 0x33))
	}
	e.words = append(e.words, rWord(0, 30, uint32(base), 0, uint32(dst), 0x33))
}
func (e *ByteAssembler) MulAdd(dst, base, index, factor machine.Register) {
	e.words = append(e.words, rWord(1, uint32(factor), uint32(index), 0, 30, 0x33))
	e.words = append(e.words, rWord(0, 30, uint32(base), 0, uint32(dst), 0x33))
}
func (e *ByteAssembler) SelectValue(dst, condition, ifTrue, ifFalse machine.Register) {
	e.words = append(e.words, bWord(12, 0, uint32(condition), 0))
	e.words = append(e.words, iWord(0, uint32(ifTrue), 0, uint32(dst), 0x13))
	e.words = append(e.words, jWord(8, 0))
	e.words = append(e.words, iWord(0, uint32(ifFalse), 0, uint32(dst), 0x13))
}
func (e *ByteAssembler) Load64(dst, address machine.Register)    { e.LoadOffset(dst, address, 0) }
func (e *ByteAssembler) Store64(address, value machine.Register) { e.StoreOffset(address, value, 0) }
func (e *ByteAssembler) Load8(dst, address machine.Register) {
	e.words = append(e.words, iWord(0, uint32(address), 4, uint32(dst), 3))
}
func (e *ByteAssembler) Store8(address, value machine.Register) {
	e.words = append(e.words, sWord(0, uint32(value), uint32(address), 0))
}
func (e *ByteAssembler) LoadAddress(dst, base, index machine.Register, scale, disp uint64) {
	e.Scaled(30, base, index, scale, 0)
	e.LoadOffset(dst, 30, disp)
}
func (e *ByteAssembler) StoreAddress(base, index, value machine.Register, scale, disp uint64) {
	e.Scaled(30, base, index, scale, 0)
	e.StoreOffset(30, value, disp)
}
func (e *ByteAssembler) memoryBase(base machine.Register, disp int64) (machine.Register, int64) {
	if disp >= -2048 && disp <= 2047 {
		return base, disp
	}
	scratch := machine.Register(30)
	if base == scratch {
		scratch = 31
	}
	e.words = append(e.words, ConstantWords(scratch, uint64(disp))...)
	e.words = append(e.words, rWord(0, uint32(scratch), uint32(base), 0, uint32(scratch), 0x33))
	return scratch, 0
}
func (e *ByteAssembler) LoadOffset(dst, base machine.Register, disp uint64) {
	base, offset := e.memoryBase(base, int64(disp))
	e.words = append(e.words, iWord(offset, uint32(base), 3, uint32(dst), 3))
}
func (e *ByteAssembler) StoreOffset(base, value machine.Register, disp uint64) {
	base, offset := e.memoryBase(base, int64(disp))
	e.words = append(e.words, sWord(offset, uint32(value), uint32(base), 3))
}
func (e *ByteAssembler) Syscall() { e.words = append(e.words, 0x00000073) }
func (e *ByteAssembler) CallC()   { e.words = append(e.words, iWord(0, 31, 0, 1, 0x67)) }
func (e *ByteAssembler) CallGo()  { e.CallC() }
func (e *ByteAssembler) SaveLink(slot int) {
	if e.stack != abi.StackPlatform {
		e.StoreOffset(9, 1, uint64(slot*8))
	}
}
func (e *ByteAssembler) RestoreLink(slot int) {
	if e.stack != abi.StackPlatform {
		e.LoadOffset(1, 9, uint64(slot*8))
	}
}
func (e *ByteAssembler) Spill(src machine.Register, slot int) {
	base, offset := machine.Register(9), slot*8
	if e.stack == abi.StackPlatform {
		base, offset = 2, (slot+1+e.goHomes)*8
	}
	e.StoreOffset(base, src, uint64(offset))
}
func (e *ByteAssembler) Reload(dst machine.Register, slot int) {
	base, offset := machine.Register(9), slot*8
	if e.stack == abi.StackPlatform {
		base, offset = 2, (slot+1+e.goHomes)*8
	}
	e.LoadOffset(dst, base, uint64(offset))
}
func (e *ByteAssembler) SaveCFloat(reg machine.Register, slot int) {
	base, offset := e.memoryBase(2, int64((slot+1+e.goHomes)*8))
	e.words = append(e.words, sWord(offset, uint32(reg), uint32(base), 3)&^uint32(0x7f)|0x27)
}
func (e *ByteAssembler) RestoreCFloat(reg machine.Register, slot int) {
	base, offset := e.memoryBase(2, int64((slot+1+e.goHomes)*8))
	e.words = append(e.words, iWord(offset, uint32(base), 3, uint32(reg), 0x07))
}
func (e *ByteAssembler) IncomingWord(dst machine.Register, entrySPOffset int) {
	e.LoadOffset(dst, 2, uint64(entrySPOffset))
}
func (e *ByteAssembler) adjustSP(frame int, subtract bool) {
	for frame > 0 {
		step := frame
		if step > 2032 {
			step = 2032
		}
		if subtract {
			step = -step
		}
		e.words = append(e.words, iWord(int64(step), 2, 0, 2, 0x13))
		if subtract {
			frame += step
		} else {
			frame -= step
		}
	}
}
func (e *ByteAssembler) FrameEnter(frame int) {
	e.adjustSP(frame, true)
	e.words = append(e.words, sWord(0, 1, 2, 3))
}
func (e *ByteAssembler) FrameLeave(frame int) {
	e.words = append(e.words, iWord(0, 2, 3, 1, 3))
	e.adjustSP(frame, false)
}
func (e *ByteAssembler) Ret() { e.words = append(e.words, iWord(0, 1, 0, 0, 0x67)) }
func (e *ByteAssembler) CompareImmediate(reg machine.Register, value int64) {
	if value == 0 {
		e.compareLeft, e.compareRight = reg, 0
		return
	}
	e.Constant(31, uint64(value))
	e.compareLeft, e.compareRight = reg, 31
}
func (e *ByteAssembler) CompareRegister(left, right machine.Register) {
	e.compareLeft, e.compareRight = left, right
}
func (e *ByteAssembler) pcRelativeJump(from, target int) {
	distance := int64(target) - int64(from)
	upper := (distance + 2048) >> 12
	lower := distance - upper<<12
	e.words = append(e.words,
		uint32(upper&0xfffff)<<12|30<<7|0x17,
		iWord(lower, 30, 0, 0, 0x67),
	)
}
func (e *ByteAssembler) Jump(target int) { e.JumpForm(target, false) }
func (e *ByteAssembler) JumpForm(target int, long bool) {
	if long {
		e.pcRelativeJump(e.pc, target)
	} else {
		e.words = append(e.words, jWord(target-e.pc, 0))
	}
}
func branchCondition(condition ir.BranchCondition, left, right machine.Register) (uint32, uint32, uint32) {
	a, b := uint32(left), uint32(right)
	switch condition {
	case ir.BranchEqual:
		return 0, a, b
	case ir.BranchNotEqual:
		return 1, a, b
	case ir.BranchLess:
		return 4, a, b
	case ir.BranchGreaterEqual:
		return 5, a, b
	case ir.BranchLessEqual:
		return 5, b, a
	case ir.BranchGreater:
		return 4, b, a
	case ir.BranchBelow:
		return 6, a, b
	case ir.BranchAboveEqual:
		return 7, a, b
	case ir.BranchBelowEqual:
		return 7, b, a
	case ir.BranchAbove:
		return 6, b, a
	default:
		return 0, a, b
	}
}
func (e *ByteAssembler) JumpIf(condition ir.BranchCondition, target int) {
	e.JumpIfForm(condition, target, true)
}
func (e *ByteAssembler) JumpIfForm(condition ir.BranchCondition, target int, long bool) {
	e.JumpIfRange(condition, target, long, false)
}
func (e *ByteAssembler) JumpIfRange(condition ir.BranchCondition, target int, long, far bool) {
	funct3, left, right := branchCondition(condition, e.compareLeft, e.compareRight)
	if !long {
		e.words = append(e.words, bWord(target-e.pc, right, left, funct3))
		return
	}
	funct3 ^= 1 // Invert the test and skip the following jump.
	if far {
		e.words = append(e.words, bWord(12, right, left, funct3))
		e.pcRelativeJump(e.pc+4, target)
	} else {
		e.words = append(e.words, bWord(8, right, left, funct3), jWord(target-(e.pc+4), 0))
	}
}

// WithCPUProfile selects validated instruction extensions.
func (e *ByteAssembler) WithCPUProfile(p abi.CPUProfile) *ByteAssembler {
	e.profile = p.Clone()
	return e
}

// AndNot emits Zbb ANDN; selection gates it on the CPU profile.
func (e *ByteAssembler) AndNot(_ uint8, d, a, b machine.Register) {
	e.words = append(e.words, rWord(0x20, uint32(b), uint32(a), 7, uint32(d), 0x33))
}

// BranchZeroRange directly tests the countdown register without a compare pseudo-op.
func (e *ByteAssembler) BranchZeroRange(reg machine.Register, _ uint8, nonzero bool, target int, long, far bool) {
	e.compareLeft, e.compareRight = reg, 0
	condition := ir.BranchEqual
	if nonzero {
		condition = ir.BranchNotEqual
	}
	e.JumpIfRange(condition, target, long, far)
}
