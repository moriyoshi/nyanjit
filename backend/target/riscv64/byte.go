package riscv64

import (
	"encoding/binary"
	"math/bits"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// ByteAssembler emits the uncompressed RV64IM instruction set.
type ByteAssembler struct {
	err                       error
	literalData               map[int]bool
	words                     []uint32
	relocations               []machine.Relocation
	stack                     abi.StackPractice
	goHomes                   int
	pc                        int
	profile                   abi.ResolvedCPU
	compareLeft, compareRight backend.Register
}

func NewByteAssembler(stack abi.StackPractice, goHomes int) *ByteAssembler {
	e := &ByteAssembler{stack: stack, goHomes: goHomes}
	e.require(goHomes >= 0 && goHomes <= 4094 && (stack == abi.StackNoUse || stack == abi.StackPlatform))
	return e
}
func (e *ByteAssembler) SetPC(pc int) {
	if !e.require(pc >= 0 && pc <= int(^uint(0)>>1)-32 && pc%4 == 0) {
		return
	}
	e.pc = pc
}
func (e *ByteAssembler) AssembledSize() int { return len(e.words) * 4 }

// Bytes returns encoded bytes, valid until the next assembler mutation.
// Treat the result as read-only; copy it to retain or modify it.
func (e *ByteAssembler) Err() error { return e.err }
func (e *ByteAssembler) Bytes() []byte {
	if e.err != nil {
		return nil
	}
	code := make([]byte, len(e.words)*4)
	for i, word := range e.words {
		binary.LittleEndian.PutUint32(code[i*4:], word)
	}
	return code
}

// Relocations returns read-only borrowed entries valid until the next assembler
// mutation. Copy the slice to retain or modify it.
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
func shiftWord(src, dst backend.Register, count uint8, right, arithmetic bool) uint32 {
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
func ConstantWords(dst backend.Register, value uint64) []uint32 {
	if !validGeneralRegisters(dst) {
		return nil
	}
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

func (e *ByteAssembler) Constant(dst backend.Register, value uint64) {
	if !e.require((validGeneralRegisters(dst))) {
		return
	}

	e.words = append(e.words, ConstantWords(dst, value)...)
}
func (e *ByteAssembler) Symbol(dst backend.Register, name string) {
	if !e.require((validGeneralRegisters(dst))) {
		return
	}

	e.relocations = append(e.relocations, machine.Relocation{Symbol: name, Offset: len(e.words) * 4, Encoding: machine.RelocRISCV64ByteSequence})
	e.words = append(e.words, iWord(0, 0, 0, uint32(dst), 0x13))
	for i := 0; i < 7; i++ {
		e.words = append(e.words, shiftWord(dst, dst, 8, false, false))
		e.words = append(e.words, iWord(0, uint32(dst), 6, uint32(dst), 0x13))
	}
}
func (e *ByteAssembler) Move(dst, src backend.Register) {
	if !e.require((validGeneralRegisters(dst, src))) {
		return
	}

	if dst != src {
		e.words = append(e.words, iWord(0, uint32(src), 0, uint32(dst), 0x13))
	}
}
func (e *ByteAssembler) Binary(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	if !e.require((validGeneralRegisters(dst, left, right)) && (width == 0 || validWidth(width)) && (validBinary(operation))) {
		return
	}

	funct7, funct3 := uint32(0), uint32(0)
	switch operation {
	case op.OpSub:
		funct7 = 0x20
	case op.OpMul:
		funct7 = 1
	case op.OpAnd:
		funct3 = 7
	case op.OpOr:
		funct3 = 6
	case op.OpXor:
		funct3 = 4
	}
	e.words = append(e.words, rWord(funct7, uint32(right), uint32(left), funct3, uint32(dst), 0x33))
}
func (e *ByteAssembler) BinaryImmediate(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	if !e.require(operation != op.OpMul) {
		return
	}
	if !e.require((validGeneralRegisters(dst, left)) && (width == 0 || validWidth(width)) && (validBinary(operation))) {
		return
	}

	immediate := int64(value)
	funct3 := uint32(0)
	switch operation {
	case op.OpSub:
		immediate = -immediate
	case op.OpAnd:
		funct3 = 7
	case op.OpOr:
		funct3 = 6
	case op.OpXor:
		funct3 = 4
	}
	if !e.require(immediate >= -2048 && immediate <= 2047 && dst >= 0 && dst <= 31 && left >= 0 && left <= 31) {
		return
	}
	e.words = append(e.words, iWord(immediate, uint32(left), funct3, uint32(dst), 0x13))
}
func (e *ByteAssembler) Decrement(dst, src backend.Register) {
	if !e.require((validGeneralRegisters(dst, src))) {
		return
	}

	e.words = append(e.words, iWord(-1, uint32(src), 0, uint32(dst), 0x13))
}
func (e *ByteAssembler) Narrow(dst, src backend.Register, width uint8) {
	if !e.require((validGeneralRegisters(dst, src)) && (width >= 1 && width <= 64)) {
		return
	}

	if !e.require(width >= 1 && width <= 64) {
		return
	}
	shift := 64 - width
	e.words = append(e.words, shiftWord(src, dst, shift, false, false), shiftWord(dst, dst, shift, true, false))
}
func (e *ByteAssembler) SignExtend(dst, src backend.Register, width uint8) {
	if !e.require((validGeneralRegisters(dst, src)) && (width >= 1 && width <= 64)) {
		return
	}

	if !e.require(width >= 1 && width <= 64) {
		return
	}
	if width == 32 {
		e.words = append(e.words, iWord(0, uint32(src), 0, uint32(dst), 0x1b))
		return
	}
	shift := 64 - width
	e.words = append(e.words, shiftWord(src, dst, shift, false, false), shiftWord(dst, dst, shift, true, true))
}
func (e *ByteAssembler) Shift(operation op.Opcode, width uint8, dst, src backend.Register, count uint8) {
	if !e.require((validGeneralRegisters(dst, src)) && (validWidth(width)) && (count < width) && (validShift(operation))) {
		return
	}

	if !e.require((width == 8 || width == 16 || width == 32 || width == 64) && count < width) {
		return
	}
	if e.profile.Has("zbb") && width >= 32 && (operation == op.OpRotateLeft || operation == op.OpRotateRight) {
		if operation == op.OpRotateLeft {
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
	case op.OpShiftLeft:
		e.words = append(e.words, shiftWord(src, dst, count, false, false))
	case op.OpShiftRight:
		e.words = append(e.words, shiftWord(src, dst, count, true, false))
	case op.OpShiftRightArithmetic:
		if width < 64 {
			e.words = append(e.words, shiftWord(src, 30, 64-width, false, false))
			e.words = append(e.words, shiftWord(30, dst, 64-width+count, true, true))
		} else {
			e.words = append(e.words, shiftWord(src, dst, count, true, true))
		}
	case op.OpRotateLeft, op.OpRotateRight:
		if count == 0 {
			e.Move(dst, src)
			return
		}
		left, right := count, width-count
		if operation == op.OpRotateRight {
			left, right = width-count, count
		}
		e.words = append(e.words, shiftWord(src, 30, left, false, false), shiftWord(src, 31, right, true, false))
		e.words = append(e.words, rWord(0, 31, 30, 6, uint32(dst), 0x33))
	}
}
func (e *ByteAssembler) Scaled(dst, base, index backend.Register, scale, disp uint64) {
	if !e.require((disp == 0) && (validGeneralRegisters(dst, base, index))) {
		return
	}

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
func (e *ByteAssembler) MulAdd(dst, base, index, factor backend.Register) {
	if !e.require((validGeneralRegisters(dst, base, index, factor))) {
		return
	}

	e.words = append(e.words, rWord(1, uint32(factor), uint32(index), 0, 30, 0x33))
	e.words = append(e.words, rWord(0, 30, uint32(base), 0, uint32(dst), 0x33))
}
func (e *ByteAssembler) SelectValue(dst, condition, ifTrue, ifFalse backend.Register) {
	if !e.require((validGeneralRegisters(dst, condition, ifTrue, ifFalse))) {
		return
	}

	e.words = append(e.words, bWord(12, 0, uint32(condition), 0))
	e.words = append(e.words, iWord(0, uint32(ifTrue), 0, uint32(dst), 0x13))
	e.words = append(e.words, jWord(8, 0))
	e.words = append(e.words, iWord(0, uint32(ifFalse), 0, uint32(dst), 0x13))
}
func (e *ByteAssembler) Load64(dst, address backend.Register) {
	if !e.require((validGeneralRegisters(dst, address))) {
		return
	}
	e.LoadOffset(dst, address, 0)
}
func (e *ByteAssembler) Store64(address, value backend.Register) {
	if !e.require((validGeneralRegisters(address, value))) {
		return
	}
	e.StoreOffset(address, value, 0)
}
func (e *ByteAssembler) Load8(dst, address backend.Register) {
	if !e.require((validGeneralRegisters(dst, address))) {
		return
	}

	e.words = append(e.words, iWord(0, uint32(address), 4, uint32(dst), 3))
}
func (e *ByteAssembler) Store8(address, value backend.Register) {
	if !e.require((validGeneralRegisters(address, value))) {
		return
	}

	e.words = append(e.words, sWord(0, uint32(value), uint32(address), 0))
}
func (e *ByteAssembler) LoadAddress(dst, base, index backend.Register, scale, disp uint64) {
	if !e.require((validGeneralRegisters(dst, base, index))) {
		return
	}

	e.Scaled(30, base, index, scale, 0)
	e.LoadOffset(dst, 30, disp)
}
func (e *ByteAssembler) StoreAddress(base, index, value backend.Register, scale, disp uint64) {
	if !e.require((validGeneralRegisters(base, index, value))) {
		return
	}

	e.Scaled(30, base, index, scale, 0)
	e.StoreOffset(30, value, disp)
}
func (e *ByteAssembler) memoryBase(base backend.Register, disp int64) (backend.Register, int64) {
	if disp >= -2048 && disp <= 2047 {
		return base, disp
	}
	scratch := backend.Register(30)
	if base == scratch {
		scratch = 31
	}
	e.words = append(e.words, ConstantWords(scratch, uint64(disp))...)
	e.words = append(e.words, rWord(0, uint32(scratch), uint32(base), 0, uint32(scratch), 0x33))
	return scratch, 0
}
func (e *ByteAssembler) LoadOffset(dst, base backend.Register, disp uint64) {
	if !e.require((validGeneralRegisters(dst, base))) {
		return
	}

	base, offset := e.memoryBase(base, int64(disp))
	e.words = append(e.words, iWord(offset, uint32(base), 3, uint32(dst), 3))
}
func (e *ByteAssembler) StoreOffset(base, value backend.Register, disp uint64) {
	if !e.require((validGeneralRegisters(base, value))) {
		return
	}

	base, offset := e.memoryBase(base, int64(disp))
	e.words = append(e.words, sWord(offset, uint32(value), uint32(base), 3))
}
func (e *ByteAssembler) Syscall() { e.words = append(e.words, 0x00000073) }
func (e *ByteAssembler) CallC()   { e.words = append(e.words, iWord(0, 31, 0, 1, 0x67)) }
func (e *ByteAssembler) CallGo()  { e.CallC() }
func (e *ByteAssembler) SaveLink(slot int) {
	if !e.require((validSlot(slot, e.goHomes))) {
		return
	}

	if e.stack != abi.StackPlatform {
		e.StoreOffset(9, 1, uint64(slot*8))
	}
}
func (e *ByteAssembler) RestoreLink(slot int) {
	if !e.require((validSlot(slot, e.goHomes))) {
		return
	}

	if e.stack != abi.StackPlatform {
		e.LoadOffset(1, 9, uint64(slot*8))
	}
}
func (e *ByteAssembler) Spill(src backend.Register, slot int) {
	if !e.require((validGeneralRegisters(src)) && (validSlot(slot, e.goHomes))) {
		return
	}

	base, offset := backend.Register(9), slot*8
	if e.stack == abi.StackPlatform {
		base, offset = 2, (slot+1+e.goHomes)*8
	}
	e.StoreOffset(base, src, uint64(offset))
}
func (e *ByteAssembler) Reload(dst backend.Register, slot int) {
	if !e.require((validGeneralRegisters(dst)) && (validSlot(slot, e.goHomes))) {
		return
	}

	base, offset := backend.Register(9), slot*8
	if e.stack == abi.StackPlatform {
		base, offset = 2, (slot+1+e.goHomes)*8
	}
	e.LoadOffset(dst, base, uint64(offset))
}
func (e *ByteAssembler) SaveCFloat(reg backend.Register, slot int) {
	if !e.require((validGeneralRegisters(reg)) && (validSlot(slot, e.goHomes))) {
		return
	}

	base, offset := e.memoryBase(2, int64((slot+1+e.goHomes)*8))
	e.words = append(e.words, sWord(offset, uint32(reg), uint32(base), 3)&^uint32(0x7f)|0x27)
}
func (e *ByteAssembler) RestoreCFloat(reg backend.Register, slot int) {
	if !e.require((validGeneralRegisters(reg)) && (validSlot(slot, e.goHomes))) {
		return
	}

	base, offset := e.memoryBase(2, int64((slot+1+e.goHomes)*8))
	e.words = append(e.words, iWord(offset, uint32(base), 3, uint32(reg), 0x07))
}
func (e *ByteAssembler) IncomingWord(dst backend.Register, entrySPOffset int) {
	if !e.require((validGeneralRegisters(dst))) {
		return
	}

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
	if !e.require((frame >= 0 && uint64(frame) <= 1<<20)) {
		return
	}

	e.adjustSP(frame, true)
	e.words = append(e.words, sWord(0, 1, 2, 3))
}
func (e *ByteAssembler) FrameLeave(frame int) {
	if !e.require((frame >= 0 && uint64(frame) <= 1<<20)) {
		return
	}

	e.words = append(e.words, iWord(0, 2, 3, 1, 3))
	e.adjustSP(frame, false)
}
func (e *ByteAssembler) Ret() { e.words = append(e.words, iWord(0, 1, 0, 0, 0x67)) }
func (e *ByteAssembler) CompareImmediate(reg backend.Register, value int64) {
	if !e.require((validGeneralRegisters(reg))) {
		return
	}

	if value == 0 {
		e.compareLeft, e.compareRight = reg, 0
		return
	}
	e.Constant(31, uint64(value))
	e.compareLeft, e.compareRight = reg, 31
}
func (e *ByteAssembler) CompareRegister(left, right backend.Register) {
	if !e.require((validGeneralRegisters(left, right))) {
		return
	}

	e.compareLeft, e.compareRight = left, right
}
func (e *ByteAssembler) pcRelativeJump(from, target int) {
	if !e.require(e.displacement(from, target, 32, 2) && int64(target)-int64(from) <= (1<<31)-2049) {
		return
	}
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
	if !e.require(long || e.displacement(e.pc, target, 21, 2)) {
		return
	}
	if long {
		e.pcRelativeJump(e.pc, target)
	} else {
		e.words = append(e.words, jWord(target-e.pc, 0))
	}
}
func branchCondition(condition op.BranchCondition, left, right backend.Register) (uint32, uint32, uint32) {
	a, b := uint32(left), uint32(right)
	switch condition {
	case op.BranchEqual:
		return 0, a, b
	case op.BranchNotEqual:
		return 1, a, b
	case op.BranchLess:
		return 4, a, b
	case op.BranchGreaterEqual:
		return 5, a, b
	case op.BranchLessEqual:
		return 5, b, a
	case op.BranchGreater:
		return 4, b, a
	case op.BranchBelow:
		return 6, a, b
	case op.BranchAboveEqual:
		return 7, a, b
	case op.BranchBelowEqual:
		return 7, b, a
	case op.BranchAbove:
		return 6, b, a
	default:
		return 0, a, b
	}
}
func (e *ByteAssembler) JumpIf(condition op.BranchCondition, target int) {
	if !e.require((condition < 12)) {
		return
	}

	e.JumpIfForm(condition, target, true)
}
func (e *ByteAssembler) JumpIfForm(condition op.BranchCondition, target int, long bool) {
	if !e.require((condition < 12)) {
		return
	}

	e.JumpIfRange(condition, target, long, false)
}
func (e *ByteAssembler) JumpIfRange(condition op.BranchCondition, target int, long, far bool) {
	if !e.require((condition < 12)) {
		return
	}

	if !e.require(condition <= 9 && ((!long && e.displacement(e.pc, target, 13, 2)) || (long && (far || e.displacement(e.pc+4, target, 21, 2))))) {
		return
	}
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

// WithCPU selects an immutable contract for riscv64 (or the zero baseline).
// It panics if p was resolved for another architecture.
func (e *ByteAssembler) WithCPU(p abi.ResolvedCPU) *ByteAssembler {
	if p.Architecture() != "" && p.Architecture() != "riscv64" {
		panic("riscv64: foreign CPU architecture")
	}
	e.profile = p
	return e
}

// AndNot emits Zbb ANDN; selection gates it on the CPU profile.
func (e *ByteAssembler) AndNot(width uint8, d, a, b backend.Register) {
	if !e.require((validGeneralRegisters(d, a, b)) && (validWidth(width))) {
		return
	}

	e.words = append(e.words, rWord(0x20, uint32(b), uint32(a), 7, uint32(d), 0x33))
}

// BranchZeroRange directly tests the countdown register without a compare pseudo-op.
func (e *ByteAssembler) BranchZeroRange(reg backend.Register, width uint8, nonzero bool, target int, long, far bool) {
	if !e.require((validGeneralRegisters(reg)) && (validWidth(width))) {
		return
	}

	e.compareLeft, e.compareRight = reg, 0
	condition := op.BranchEqual
	if nonzero {
		condition = op.BranchNotEqual
	}
	e.JumpIfRange(condition, target, long, far)
}
