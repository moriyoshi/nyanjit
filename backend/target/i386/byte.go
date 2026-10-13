package i386

import (
	"math"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// ByteAssembler encodes the 32-bit x86 subset selected by the 386 target.
type ByteAssembler struct {
	err         error
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

// Bytes returns encoded bytes, valid until the next assembler mutation.
// Treat the result as read-only; copy it to retain or modify it.
func (e *ByteAssembler) Bytes() []byte {
	if e.err != nil {
		return nil
	}
	return e.code
}

// Relocations returns read-only borrowed entries valid until the next assembler
// mutation. Copy the slice to retain or modify it.
func (e *ByteAssembler) Relocations() []machine.Relocation { return e.relocations }
func (e *ByteAssembler) Constant(dst backend.Register, value uint64) {
	if !e.require(validGeneralRegisters(dst) && fitsWord(value)) {
		return
	}

	e.code = append32(append(e.code, 0xb8|byte(dst)), uint32(value))
}
func (e *ByteAssembler) Symbol(dst backend.Register, name string) {
	if !e.require(validGeneralRegisters(dst)) {
		return
	}

	e.relocations = append(e.relocations, machine.Relocation{Symbol: name, Offset: len(e.code), Encoding: machine.Reloc386Immediate})
	e.Constant(dst, 0)
}
func (e *ByteAssembler) Move(dst, src backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	if dst != src {
		e.code = append(e.code, 0x89, modRM(3, byte(src), byte(dst)))
	}
}
func (e *ByteAssembler) Binary(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(left) && validGeneralRegisters(right) && validBinary(operation, width)) {
		return
	}

	e.Move(dst, left)
	if operation == op.OpMul {
		e.code = append(e.code, 0x0f, 0xaf, modRM(3, byte(dst), byte(right)))
	} else {
		opcode := aluOpcode(operation)
		e.code = append(e.code, opcode, modRM(3, byte(right), byte(dst)))
	}
	if width == 8 || width == 16 {
		e.mask(dst, width)
	}
}
func (e *ByteAssembler) BinaryImmediate(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(left) && validBinaryImmediate(operation, width, value)) {
		return
	}

	e.Move(dst, left)
	group := aluGroup(operation)
	e.code = append32(append(e.code, 0x81, modRM(3, group, byte(dst))), uint32(value))
}

// BinaryCarry is a compatibility visitor for the shared word-carry operation.
func (e *ByteAssembler) BinaryCarry(operation op.Opcode, dst, left, right backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(left) && validGeneralRegisters(right)) {
		return
	}

	e.WordCarry(machine.Instruction{Op: machine.OpWordCarry, Arithmetic: operation, Bits: 32, A: dst, B: left, C: right, CarryMode: machine.CarryFlags})
}

// MulWide computes the unsigned EDX:EAX product of EAX and right.
func (e *ByteAssembler) MulWide(right backend.Register) {
	if !e.require(validGeneralRegisters(right)) {
		return
	}

	e.code = append(e.code, 0xf7, modRM(3, 4, byte(right)))
}
func (e *ByteAssembler) Decrement(dst, src backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	e.Move(dst, src)
	e.code = append(e.code, 0xff, modRM(3, 1, byte(dst)))
}
func (e *ByteAssembler) Increment(dst, src backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	e.Move(dst, src)
	e.code = append(e.code, 0xff, modRM(3, 0, byte(dst)))
}
func (e *ByteAssembler) mask(dst backend.Register, width uint8) {
	if width == 8 {
		e.code = append(e.code, 0x81, modRM(3, 4, byte(dst)), 0xff, 0, 0, 0)
	} else if width == 16 {
		e.code = append(e.code, 0x81, modRM(3, 4, byte(dst)), 0xff, 0xff, 0, 0)
	}
}
func (e *ByteAssembler) Narrow(dst, src backend.Register, width uint8) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	e.Move(dst, src)
	e.mask(dst, width)
}
func (e *ByteAssembler) SignExtend(dst, src backend.Register, width uint8) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	e.Move(dst, src)
	if width == 8 || width == 16 {
		shift := byte(32 - width)
		e.code = append(e.code, 0xc1, modRM(3, 4, byte(dst)), shift)
		e.code = append(e.code, 0xc1, modRM(3, 7, byte(dst)), shift)
	}
}
func (e *ByteAssembler) Shift(operation op.Opcode, width uint8, dst, src backend.Register, count uint8) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(src)) {
		return
	}

	e.Move(dst, src)
	if width < 32 && operation == op.OpShiftRightArithmetic {
		e.SignExtend(dst, dst, width)
	}
	group := shiftGroup(operation)
	e.code = append(e.code, 0xc1, modRM(3, group, byte(dst)), count)
	if width < 32 && operation != op.OpShiftRightArithmetic {
		e.mask(dst, width)
	}
}
func (e *ByteAssembler) Scaled(dst, base, index backend.Register, scale, disp uint64) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(base) && validGeneralRegisters(index)) {
		return
	}

	if !e.require(validIndexed(index, scale, disp)) {
		return
	}
	e.code = appendIndexed(e.code, 0x8d, dst, base, index, scale, disp)
}
func (e *ByteAssembler) SelectValue(dst, condition, ifTrue, ifFalse backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(condition) && validGeneralRegisters(ifTrue) && validGeneralRegisters(ifFalse)) {
		return
	}

	e.Move(dst, ifFalse)
	e.code = append(e.code, 0x85, modRM(3, byte(condition), byte(condition)))
	e.code = append(e.code, 0x0f, 0x45, modRM(3, byte(dst), byte(ifTrue)))
}
func (e *ByteAssembler) Load64(dst, address backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(address)) {
		return
	}

	e.code = appendBase(e.code, 0x8b, dst, address, 0)
}
func (e *ByteAssembler) Store64(address, value backend.Register) {
	if !e.require(validGeneralRegisters(address) && validGeneralRegisters(value)) {
		return
	}

	e.code = appendBase(e.code, 0x89, value, address, 0)
}
func (e *ByteAssembler) Load8(dst, address backend.Register) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(address)) {
		return
	}

	e.code = append(e.code, 0x0f)
	e.code = appendBase(e.code, 0xb6, dst, address, 0)
}
func (e *ByteAssembler) Store8(address, value backend.Register) {
	// Only AL, CL, DL and BL name the low byte of a general register. Codes
	// 4-7 would select AH, CH, DH and BH instead of ESP, EBP, ESI and EDI.
	if !e.require(validGeneralRegisters(address) && validGeneralRegisters(value) && value < 4) {
		return
	}

	e.code = appendBase(e.code, 0x88, value, address, 0)
}
func (e *ByteAssembler) LoadAddress(dst, base, index backend.Register, scale, disp uint64) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(base) && validGeneralRegisters(index)) {
		return
	}

	if !e.require(validIndexed(index, scale, disp)) {
		return
	}
	e.code = appendIndexed(e.code, 0x8b, dst, base, index, scale, disp)
}
func (e *ByteAssembler) StoreAddress(base, index, value backend.Register, scale, disp uint64) {
	if !e.require(validGeneralRegisters(base) && validGeneralRegisters(index) && validGeneralRegisters(value)) {
		return
	}

	if !e.require(validIndexed(index, scale, disp)) {
		return
	}
	e.code = appendIndexed(e.code, 0x89, value, base, index, scale, disp)
}
func (e *ByteAssembler) LoadOffset(dst, base backend.Register, disp uint64) {
	if !e.require(validGeneralRegisters(dst) && validGeneralRegisters(base)) {
		return
	}

	if !e.require(fitsWord(disp)) {
		return
	}
	e.code = appendBase(e.code, 0x8b, dst, base, disp)
}
func (e *ByteAssembler) StoreOffset(base, value backend.Register, disp uint64) {
	if !e.require(validGeneralRegisters(base) && validGeneralRegisters(value)) {
		return
	}

	if !e.require(fitsWord(disp)) {
		return
	}
	e.code = appendBase(e.code, 0x89, value, base, disp)
}
func (e *ByteAssembler) Syscall()      { e.code = append(e.code, 0xcd, 0x80) }
func (e *ByteAssembler) CallC()        { e.code = append(e.code, 0xff, 0xd0) }
func (e *ByteAssembler) CallGo()       { e.CallC() }
func (*ByteAssembler) SaveLink(int)    {}
func (*ByteAssembler) RestoreLink(int) {}
func (e *ByteAssembler) Spill(src backend.Register, slot int) {
	if !e.require(validGeneralRegisters(src)) {
		return
	}

	base := backend.Register(5) // EBP is a caller-supplied workspace pointer without SP.
	if e.stack == abi.StackPlatform {
		base = 4
	}
	e.code = appendBase32(e.code, 0x89, src, base, e.callArea+slot*4)
}
func (e *ByteAssembler) Reload(dst backend.Register, slot int) {
	if !e.require(validGeneralRegisters(dst)) {
		return
	}

	base := backend.Register(5)
	if e.stack == abi.StackPlatform {
		base = 4
	}
	e.code = appendBase32(e.code, 0x8b, dst, base, e.callArea+slot*4)
}
func (e *ByteAssembler) IncomingWord(dst backend.Register, entrySPOffset int) {
	if !e.require(validGeneralRegisters(dst)) {
		return
	}

	e.code = appendBase32(e.code, 0x8b, dst, 4, e.frame+entrySPOffset)
}
func (e *ByteAssembler) FrameEnter(frame int) { e.FrameEnterArea(frame, 0) }

// FrameEnterArea reserves the specified outgoing call area below spill slots.
func (e *ByteAssembler) FrameEnterArea(frame, callArea int) {
	if !e.require(frame >= 16 && callArea >= 0) { // the four pushes are part of frame
		return
	}
	e.frame = frame
	e.callArea = callArea
	e.code = append(e.code, 0x53, 0x56, 0x57, 0x55) // preserve EBX, ESI, EDI, EBP
	e.code = append32(append(e.code, 0x81, 0xec), uint32(frame-16))
}
func (e *ByteAssembler) FrameLeave(frame int) {
	if !e.require(frame >= 16) {
		return
	}
	e.code = append32(append(e.code, 0x81, 0xc4), uint32(frame-16))
	e.code = append(e.code, 0x5d, 0x5f, 0x5e, 0x5b)
}
func (e *ByteAssembler) Ret() { e.code = append(e.code, 0xc3) }
func (e *ByteAssembler) CompareImmediate(reg backend.Register, value int64) {
	if !e.require(validGeneralRegisters(reg)) {
		return
	}

	e.code = append32(append(e.code, 0x81, modRM(3, 7, byte(reg))), uint32(value))
}
func (e *ByteAssembler) CompareRegister(left, right backend.Register) {
	if !e.require(validGeneralRegisters(left) && validGeneralRegisters(right)) {
		return
	}

	e.code = append(e.code, 0x39, modRM(3, byte(right), byte(left)))
}
func (e *ByteAssembler) Jump(target int) {
	e.code = append32(append(e.code, 0xe9), uint32(int32(target-(e.pc+5))))
}
func (e *ByteAssembler) JumpIf(condition op.BranchCondition, target int) {
	if !e.require(condition < 12) {
		return
	}
	opcode := [...]byte{0x84, 0x85, 0x8c, 0x8d, 0x8e, 0x8f, 0x82, 0x83, 0x86, 0x87, 0x88, 0x89}[condition]
	e.code = append32(append(e.code, 0x0f, opcode), uint32(int32(target-(e.pc+6))))
}

func modRM(mode, reg, rm byte) byte { return mode<<6 | reg<<3 | rm }
func append32(code []byte, value uint32) []byte {
	return append(code, byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
}
func appendBase(code []byte, opcode byte, data, base backend.Register, disp uint64) []byte {
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
func appendBase32(code []byte, opcode byte, data, base backend.Register, disp int) []byte {
	code = append(code, opcode, modRM(2, byte(data), byte(base)))
	if base == 4 {
		code = append(code, 0x24)
	}
	return append32(code, uint32(disp))
}

// validBinary reports whether Binary encodes operation at width; 8- and
// 16-bit results are masked, and 64-bit values are register pairs.
func validBinary(operation op.Opcode, width uint8) bool {
	switch operation {
	case op.OpAdd, op.OpSub, op.OpAnd, op.OpOr, op.OpXor, op.OpMul:
		return width == 0 || width == 8 || width == 16 || width == 32
	}
	return false
}

// validBinaryImmediate reports whether BinaryImmediate encodes the group-1
// operation with an imm32. Bits above an 8- or 16-bit width are not masked.
func validBinaryImmediate(operation op.Opcode, width uint8, value uint64) bool {
	return operation != op.OpMul && validBinary(operation, width) && fitsWord(value)
}

// validIndexed reports whether a SIB operand can encode index*scale+disp:
// ESP means no index.
func validIndexed(index backend.Register, scale, disp uint64) bool {
	return index != 4 && (scale == 1 || scale == 2 || scale == 4 || scale == 8) && fitsWord(disp)
}

// fitsWord accepts 32-bit values, unsigned or sign-extended.
func fitsWord(value uint64) bool {
	return value <= math.MaxUint32 || int64(value) >= math.MinInt32 && int64(value) < 0
}

func appendIndexed(code []byte, opcode byte, data, base, index backend.Register, scale, disp uint64) []byte {
	scaleBits := scaleField(scale)
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

// aluOpcode is the register-register opcode of a two-operand ALU operation.
func aluOpcode(operation op.Opcode) byte {
	switch operation {
	case op.OpAdd:
		return 0x01
	case op.OpSub:
		return 0x29
	case op.OpAnd:
		return 0x21
	case op.OpOr:
		return 0x09
	case op.OpXor:
		return 0x31
	}
	return 0
}

// aluGroup is the ModRM reg field of an ALU operation with an immediate.
func aluGroup(operation op.Opcode) byte {
	switch operation {
	case op.OpOr:
		return 1
	case op.OpAnd:
		return 4
	case op.OpSub:
		return 5
	case op.OpXor:
		return 6
	}
	return 0
}

// shiftGroup is the ModRM reg field of a shift or rotate.
func shiftGroup(operation op.Opcode) byte {
	switch operation {
	case op.OpRotateRight:
		return 1
	case op.OpShiftLeft:
		return 4
	case op.OpShiftRight:
		return 5
	case op.OpShiftRightArithmetic:
		return 7
	}
	return 0
}

// scaleField is the SIB scale field of an index scale.
func scaleField(scale uint64) byte {
	switch scale {
	case 2:
		return 1
	case 4:
		return 2
	case 8:
		return 3
	}
	return 0
}
