package i386

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// ByteAssembler encodes the 32-bit x86 subset selected by the 386 target.
type ByteAssembler struct {
	code        []byte
	relocations []machine.Relocation
	stack       abi.StackPractice
	frame       int
	callArea    int
	pc          int
}

func NewByteAssembler(stack abi.StackPractice) *ByteAssembler { return &ByteAssembler{stack: stack} }
func (e *ByteAssembler) SetPC(pc int)                         { e.pc = pc }
func (e *ByteAssembler) AssembledSize() int                   { return len(e.code) }
func (e *ByteAssembler) Bytes() []byte                        { return e.code }
func (e *ByteAssembler) Relocations() []machine.Relocation    { return e.relocations }
func (e *ByteAssembler) Constant(dst machine.Register, value uint64) {
	e.code = append32(append(e.code, 0xb8|byte(dst)), uint32(value))
}
func (e *ByteAssembler) Symbol(dst machine.Register, name string) {
	e.relocations = append(e.relocations, machine.Relocation{Symbol: name, Offset: len(e.code), Encoding: machine.Reloc386Immediate})
	e.Constant(dst, 0)
}
func (e *ByteAssembler) Move(dst, src machine.Register) {
	if dst != src {
		e.code = append(e.code, 0x89, modRM(3, byte(src), byte(dst)))
	}
}
func (e *ByteAssembler) Binary(operation ir.Opcode, width uint8, dst, left, right machine.Register) {
	e.Move(dst, left)
	if operation == ir.OpMul {
		e.code = append(e.code, 0x0f, 0xaf, modRM(3, byte(dst), byte(right)))
	} else {
		opcode := map[ir.Opcode]byte{ir.OpAdd: 0x01, ir.OpSub: 0x29, ir.OpAnd: 0x21, ir.OpOr: 0x09, ir.OpXor: 0x31}[operation]
		e.code = append(e.code, opcode, modRM(3, byte(right), byte(dst)))
	}
	if width == 8 || width == 16 {
		e.mask(dst, width)
	}
}
func (e *ByteAssembler) BinaryImmediate(operation ir.Opcode, _ uint8, dst, left machine.Register, value uint64) {
	e.Move(dst, left)
	group := map[ir.Opcode]byte{ir.OpAdd: 0, ir.OpOr: 1, ir.OpAnd: 4, ir.OpSub: 5, ir.OpXor: 6}[operation]
	e.code = append32(append(e.code, 0x81, modRM(3, group, byte(dst))), uint32(value))
}

// BinaryCarry is a compatibility visitor for the shared word-carry operation.
func (e *ByteAssembler) BinaryCarry(operation ir.Opcode, dst, left, right machine.Register) {
	e.WordCarry(machine.Instruction{Op: machine.OpWordCarry, Arithmetic: operation, Bits: 32, A: dst, B: left, C: right, CarryMode: machine.CarryFlags})
}

// MulWide computes the unsigned EDX:EAX product of EAX and right.
func (e *ByteAssembler) MulWide(right machine.Register) {
	e.code = append(e.code, 0xf7, modRM(3, 4, byte(right)))
}
func (e *ByteAssembler) Decrement(dst, src machine.Register) {
	e.Move(dst, src)
	e.code = append(e.code, 0xff, modRM(3, 1, byte(dst)))
}
func (e *ByteAssembler) Increment(dst, src machine.Register) {
	e.Move(dst, src)
	e.code = append(e.code, 0xff, modRM(3, 0, byte(dst)))
}
func (e *ByteAssembler) mask(dst machine.Register, width uint8) {
	if width == 8 {
		e.code = append(e.code, 0x81, modRM(3, 4, byte(dst)), 0xff, 0, 0, 0)
	} else if width == 16 {
		e.code = append(e.code, 0x81, modRM(3, 4, byte(dst)), 0xff, 0xff, 0, 0)
	}
}
func (e *ByteAssembler) Narrow(dst, src machine.Register, width uint8) {
	e.Move(dst, src)
	e.mask(dst, width)
}
func (e *ByteAssembler) SignExtend(dst, src machine.Register, width uint8) {
	e.Move(dst, src)
	if width == 8 || width == 16 {
		shift := byte(32 - width)
		e.code = append(e.code, 0xc1, modRM(3, 4, byte(dst)), shift)
		e.code = append(e.code, 0xc1, modRM(3, 7, byte(dst)), shift)
	}
}
func (e *ByteAssembler) Shift(operation ir.Opcode, width uint8, dst, src machine.Register, count uint8) {
	e.Move(dst, src)
	if width < 32 && operation == ir.OpShiftRightArithmetic {
		e.SignExtend(dst, dst, width)
	}
	group := map[ir.Opcode]byte{ir.OpRotateLeft: 0, ir.OpRotateRight: 1, ir.OpShiftLeft: 4, ir.OpShiftRight: 5, ir.OpShiftRightArithmetic: 7}[operation]
	e.code = append(e.code, 0xc1, modRM(3, group, byte(dst)), count)
	if width < 32 && operation != ir.OpShiftRightArithmetic {
		e.mask(dst, width)
	}
}
func (e *ByteAssembler) Scaled(dst, base, index machine.Register, scale, disp uint64) {
	e.code = appendIndexed(e.code, 0x8d, dst, base, index, scale, disp)
}
func (e *ByteAssembler) SelectValue(dst, condition, ifTrue, ifFalse machine.Register) {
	e.Move(dst, ifFalse)
	e.code = append(e.code, 0x85, modRM(3, byte(condition), byte(condition)))
	e.code = append(e.code, 0x0f, 0x45, modRM(3, byte(dst), byte(ifTrue)))
}
func (e *ByteAssembler) Load64(dst, address machine.Register) {
	e.code = appendBase(e.code, 0x8b, dst, address, 0)
}
func (e *ByteAssembler) Store64(address, value machine.Register) {
	e.code = appendBase(e.code, 0x89, value, address, 0)
}
func (e *ByteAssembler) Load8(dst, address machine.Register) {
	e.code = append(e.code, 0x0f)
	e.code = appendBase(e.code, 0xb6, dst, address, 0)
}
func (e *ByteAssembler) Store8(address, value machine.Register) {
	e.code = appendBase(e.code, 0x88, value, address, 0)
}
func (e *ByteAssembler) LoadAddress(dst, base, index machine.Register, scale, disp uint64) {
	e.code = appendIndexed(e.code, 0x8b, dst, base, index, scale, disp)
}
func (e *ByteAssembler) StoreAddress(base, index, value machine.Register, scale, disp uint64) {
	e.code = appendIndexed(e.code, 0x89, value, base, index, scale, disp)
}
func (e *ByteAssembler) LoadOffset(dst, base machine.Register, disp uint64) {
	e.code = appendBase(e.code, 0x8b, dst, base, disp)
}
func (e *ByteAssembler) StoreOffset(base, value machine.Register, disp uint64) {
	e.code = appendBase(e.code, 0x89, value, base, disp)
}
func (e *ByteAssembler) Syscall()      { e.code = append(e.code, 0xcd, 0x80) }
func (e *ByteAssembler) CallC()        { e.code = append(e.code, 0xff, 0xd0) }
func (e *ByteAssembler) CallGo()       { e.CallC() }
func (*ByteAssembler) SaveLink(int)    {}
func (*ByteAssembler) RestoreLink(int) {}
func (e *ByteAssembler) Spill(src machine.Register, slot int) {
	base := machine.Register(5) // EBP is a caller-supplied workspace pointer without SP.
	if e.stack == abi.StackPlatform {
		base = 4
	}
	e.code = appendBase32(e.code, 0x89, src, base, e.callArea+slot*4)
}
func (e *ByteAssembler) Reload(dst machine.Register, slot int) {
	base := machine.Register(5)
	if e.stack == abi.StackPlatform {
		base = 4
	}
	e.code = appendBase32(e.code, 0x8b, dst, base, e.callArea+slot*4)
}
func (e *ByteAssembler) IncomingWord(dst machine.Register, entrySPOffset int) {
	e.code = appendBase32(e.code, 0x8b, dst, 4, e.frame+entrySPOffset)
}
func (e *ByteAssembler) FrameEnter(frame int) { e.FrameEnterArea(frame, 0) }

// FrameEnterArea reserves the specified outgoing call area below spill slots.
func (e *ByteAssembler) FrameEnterArea(frame, callArea int) {
	e.frame = frame
	e.callArea = callArea
	e.code = append(e.code, 0x53, 0x56, 0x57, 0x55) // preserve EBX, ESI, EDI, EBP
	e.code = append32(append(e.code, 0x81, 0xec), uint32(frame-16))
}
func (e *ByteAssembler) FrameLeave(frame int) {
	e.code = append32(append(e.code, 0x81, 0xc4), uint32(frame-16))
	e.code = append(e.code, 0x5d, 0x5f, 0x5e, 0x5b)
}
func (e *ByteAssembler) Ret() { e.code = append(e.code, 0xc3) }
func (e *ByteAssembler) CompareImmediate(reg machine.Register, value int64) {
	e.code = append32(append(e.code, 0x81, modRM(3, 7, byte(reg))), uint32(value))
}
func (e *ByteAssembler) CompareRegister(left, right machine.Register) {
	e.code = append(e.code, 0x39, modRM(3, byte(right), byte(left)))
}
func (e *ByteAssembler) Jump(target int) {
	e.code = append32(append(e.code, 0xe9), uint32(int32(target-(e.pc+5))))
}
func (e *ByteAssembler) JumpIf(condition ir.BranchCondition, target int) {
	opcode := [...]byte{0x84, 0x85, 0x8c, 0x8d, 0x8e, 0x8f, 0x82, 0x83, 0x86, 0x87, 0x88, 0x89}[condition]
	e.code = append32(append(e.code, 0x0f, opcode), uint32(int32(target-(e.pc+6))))
}

func modRM(mode, reg, rm byte) byte { return mode<<6 | reg<<3 | rm }
func append32(code []byte, value uint32) []byte {
	return append(code, byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
}
func appendBase(code []byte, opcode byte, data, base machine.Register, disp uint64) []byte {
	mode := byte(0)
	if disp == 0 && base != 5 {
		mode = 0
	} else if int64(int32(disp)) >= -128 && int64(int32(disp)) <= 127 {
		mode = 1
	} else {
		mode = 2
	}
	rm := byte(base)
	if base == 4 {
		rm = 4
	}
	code = append(code, opcode, modRM(mode, byte(data), rm))
	if base == 4 {
		code = append(code, 0x24)
	}
	if mode == 1 {
		return append(code, byte(disp))
	}
	if mode == 2 {
		return append32(code, uint32(disp))
	}
	return code
}
func appendBase32(code []byte, opcode byte, data, base machine.Register, disp int) []byte {
	code = append(code, opcode, modRM(2, byte(data), byte(base)))
	if base == 4 {
		code = append(code, 0x24)
	}
	return append32(code, uint32(disp))
}
func appendIndexed(code []byte, opcode byte, data, base, index machine.Register, scale, disp uint64) []byte {
	scaleBits := map[uint64]byte{1: 0, 2: 1, 4: 2, 8: 3}[scale]
	mode := byte(0)
	if disp == 0 && base != 5 {
		mode = 0
	} else if int32(disp) >= -128 && int32(disp) <= 127 {
		mode = 1
	} else {
		mode = 2
	}
	code = append(code, opcode, modRM(mode, byte(data), 4), scaleBits<<6|byte(index)<<3|byte(base))
	if mode == 1 {
		return append(code, byte(disp))
	}
	if mode == 2 {
		return append32(code, uint32(disp))
	}
	return code
}
