package amd64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func NewByteAssembler(stack abi.StackPractice, goHomes int) *ByteAssembler {
	e := &ByteAssembler{stack: stack, goHomes: goHomes}
	e.require(goHomes >= 0 && goHomes <= 4094 && (stack == abi.StackNoUse || stack == abi.StackPlatform))
	return e
}

type ByteAssembler struct {
	err         error
	code        []byte
	relocations []machine.Relocation
	stack       abi.StackPractice
	goHomes     int
	pc          int
	profile     abi.ResolvedCPU
}

func (e *ByteAssembler) Constant(dst backend.Register, value uint64) {
	if !e.require((validGeneralRegisters(dst))) {
		return
	}

	e.code = appendMovImm64(e.code, uint8(dst), value)
}
func (e *ByteAssembler) Symbol(dst backend.Register, name string) {
	if !e.require((validGeneralRegisters(dst))) {
		return
	}

	e.relocations = append(e.relocations, machine.Relocation{Symbol: name, Offset: len(e.code), Encoding: machine.RelocAMD64Immediate})
	e.code = append(e.code, 0x48|byte(dst>>3), 0xB8|byte(dst&7), 0, 0, 0, 0, 0, 0, 0, 0)
}

func (e *ByteAssembler) Move(dst, src backend.Register) {
	if !e.require((validGeneralRegisters(dst, src))) {
		return
	}

	e.code = appendMovReg(e.code, uint8(dst), uint8(src))
}

func (e *ByteAssembler) Binary(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	if !e.require((validGeneralRegisters(dst, left, right)) && (width == 0 || validWidth(width)) && (validBinary(operation))) {
		return
	}

	if width == 0 || width == 64 || width == 8 && operation == op.OpMul {
		e.code = appendBinary(e.code, operation, uint8(dst), uint8(left), uint8(right))
		return
	}
	e.code = appendBinaryNarrow(e.code, operation, width, uint8(dst), uint8(left), uint8(right))
}
func (e *ByteAssembler) BinaryImmediate(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	if !e.require(operation != op.OpMul) {
		return
	}
	if !e.require((validGeneralRegisters(dst, left)) && (width == 0 || validWidth(width)) && (validBinary(operation))) {
		return
	}

	if !e.require((Policy{}).CanBinaryImmediate(operation, width, value) && dst >= 0 && dst <= 15 && left >= 0 && left <= 15) {
		return
	}
	e.Move(dst, left)
	var group byte // ADD /0
	switch operation {
	case op.OpOr:
		group = 1
	case op.OpAnd:
		group = 4
	case op.OpSub:
		group = 5
	case op.OpXor:
		group = 6
	}
	rex := byte(0x40) | byte(dst>>3)
	if width == 64 {
		rex |= 8
	}
	e.code = append(e.code, rex)
	if int64(value) >= -128 && int64(value) <= 127 {
		e.code = append(e.code, 0x83, 0xC0|group<<3|byte(dst&7), byte(value))
	} else {
		e.code = appendDisp32(append(e.code, 0x81, 0xC0|group<<3|byte(dst&7)), uint32(value))
	}
}
func (e *ByteAssembler) Narrow(dst, src backend.Register, width uint8) {
	if !e.require((validGeneralRegisters(dst, src)) && (validWidth(width))) {
		return
	}

	if width == 64 {
		e.Move(dst, src)
		return
	}
	e.code = appendNarrow(e.code, dst, src, width)
}

// appendNarrow appends the zero extension of the low 8, 16 or 32 bits of src
// into dst.
func appendNarrow(code []byte, dst, src backend.Register, width uint8) []byte {
	rex := byte(0x40) | byte(dst>>3)<<2 | byte(src>>3)
	registers := byte(0xC0) | byte(dst&7)<<3 | byte(src&7)
	switch width {
	case 8:
		return append(code, rex, 0x0F, 0xB6, registers)
	case 16:
		return append(code, rex, 0x0F, 0xB7, registers)
	}
	return append(code, rex, 0x8B, registers)
}
func (e *ByteAssembler) SignExtend(dst, src backend.Register, width uint8) {
	if !e.require((validGeneralRegisters(dst, src)) && (validWidth(width))) {
		return
	}

	rex := byte(0x48) | byte(dst>>3)<<2 | byte(src>>3)
	registers := byte(0xC0) | byte(dst&7)<<3 | byte(src&7)
	switch width {
	case 64:
		e.Move(dst, src)
	case 8:
		e.code = append(e.code, rex, 0x0F, 0xBE, registers)
	case 16:
		e.code = append(e.code, rex, 0x0F, 0xBF, registers)
	case 32:
		e.code = append(e.code, rex, 0x63, registers)
	}
}
func (e *ByteAssembler) Decrement(dst, src backend.Register) {
	if !e.require((validGeneralRegisters(dst, src))) {
		return
	}

	e.code = appendMovReg(e.code, uint8(dst), uint8(src))
	e.code = append(e.code, 0x48|byte(dst>>3), 0xFF, 0xC8|byte(dst&7))
}
func (e *ByteAssembler) Increment(dst, src backend.Register) {
	if !e.require((validGeneralRegisters(dst, src))) {
		return
	}

	e.code = appendMovReg(e.code, uint8(dst), uint8(src))
	e.code = append(e.code, 0x48|byte(dst>>3), 0xFF, 0xC0|byte(dst&7))
}
func (e *ByteAssembler) Shift(operation op.Opcode, width uint8, dst, src backend.Register, count uint8) {
	if !e.require((validGeneralRegisters(dst, src)) && (validWidth(width)) && (count < width) && (validShift(operation))) {
		return
	}

	if code := rorxPlan(operation, width, dst, src, count, e.profile); code != nil {
		e.code = append(e.code, code...)
		return
	}
	e.code = appendMovReg(e.code, uint8(dst), uint8(src))
	if width == 16 {
		e.code = append(e.code, 0x66)
	}
	rex := byte(0x40) | byte(dst>>3)
	if width == 64 {
		rex |= 8
	}
	group := byte(0)
	switch operation {
	case op.OpRotateLeft:
		group = 0
	case op.OpRotateRight:
		group = 1
	case op.OpShiftLeft:
		group = 4
	case op.OpShiftRight:
		group = 5
	case op.OpShiftRightArithmetic:
		group = 7
	}
	opcode := byte(0xC1)
	if width == 8 {
		opcode = 0xC0
	}
	e.code = append(e.code, rex, opcode, 0xC0|group<<3|byte(dst&7), count)
}

func (e *ByteAssembler) Scaled(dst, base, index backend.Register, scale, disp uint64) {
	if !e.require((validGeneralRegisters(dst, base, index))) {
		return
	}

	if !e.require(validIndexed(index, scale, disp)) {
		return
	}
	e.code = appendIndexedMemory(e.code, 0x8D, dst, base, index, scale, disp)
}

// validIndexed reports whether a SIB operand can encode index*scale+disp:
// RSP means no index, and the displacement is a signed 32-bit field.
func validIndexed(index backend.Register, scale, disp uint64) bool {
	return index >= 0 && index <= 15 && index != 4 && (scale == 1 || scale == 2 || scale == 4 || scale == 8) &&
		int64(disp) >= -1<<31 && int64(disp) <= 1<<31-1
}

func (e *ByteAssembler) SelectValue(dst, condition, ifTrue, ifFalse backend.Register) {
	if !e.require((validGeneralRegisters(dst, condition, ifTrue, ifFalse))) {
		return
	}

	e.code = appendMovReg(e.code, uint8(dst), uint8(ifFalse))
	e.code = appendTestReg(e.code, uint8(condition))
	e.code = appendCMovNE(e.code, uint8(dst), uint8(ifTrue))
}

func (e *ByteAssembler) Load64(dst, address backend.Register) {
	if !e.require((validGeneralRegisters(dst, address))) {
		return
	}

	e.code = appendMemoryReg(e.code, 0x8B, uint8(dst), uint8(address))
}

func (e *ByteAssembler) Store64(address, value backend.Register) {
	if !e.require((validGeneralRegisters(address, value))) {
		return
	}

	e.code = appendMemoryReg(e.code, 0x89, uint8(value), uint8(address))
}

func (e *ByteAssembler) Load8(dst, address backend.Register) {
	if !e.require((validGeneralRegisters(dst, address))) {
		return
	}

	e.code = appendREX(e.code, uint8(dst), uint8(address))
	base := address & 7
	switch base {
	case 4:
		e.code = append(e.code, 0x0F, 0xB6, byte(dst&7)<<3|4, 0x24)
	case 5:
		e.code = append(e.code, 0x0F, 0xB6, 0x40|byte(dst&7)<<3|5, 0)
	default:
		e.code = append(e.code, 0x0F, 0xB6, byte(dst&7)<<3|byte(base))
	}
}

func (e *ByteAssembler) Store8(address, value backend.Register) {
	if !e.require((validGeneralRegisters(address, value))) {
		return
	}

	rex := byte(0x40)
	if value >= 8 {
		rex |= 4
	}
	if address >= 8 {
		rex |= 1
	}
	e.code = append(e.code, rex)
	base := address & 7
	switch base {
	case 4:
		e.code = append(e.code, 0x88, byte(value&7)<<3|4, 0x24)
	case 5:
		e.code = append(e.code, 0x88, 0x40|byte(value&7)<<3|5, 0)
	default:
		e.code = append(e.code, 0x88, byte(value&7)<<3|byte(base))
	}
}

func (e *ByteAssembler) LoadAddress(dst, base, index backend.Register, scale, disp uint64) {
	if !e.require((validGeneralRegisters(dst, base, index))) {
		return
	}

	if !e.require(validIndexed(index, scale, disp)) {
		return
	}
	e.code = appendIndexedMemory(e.code, 0x8B, dst, base, index, scale, disp)
}

func (e *ByteAssembler) StoreAddress(base, index, value backend.Register, scale, disp uint64) {
	if !e.require((validGeneralRegisters(base, index, value))) {
		return
	}

	if !e.require(validIndexed(index, scale, disp)) {
		return
	}
	e.code = appendIndexedMemory(e.code, 0x89, value, base, index, scale, disp)
}

func (e *ByteAssembler) LoadOffset(dst, base backend.Register, disp uint64) {
	if !e.require((validGeneralRegisters(dst, base))) {
		return
	}

	if !e.require(int64(disp) >= -1<<31 && int64(disp) <= 1<<31-1) {
		return
	}
	e.code = appendBaseOffsetMemory(e.code, 0x8B, dst, base, disp)
}

func (e *ByteAssembler) StoreOffset(base, value backend.Register, disp uint64) {
	if !e.require((validGeneralRegisters(base, value))) {
		return
	}

	if !e.require(int64(disp) >= -1<<31 && int64(disp) <= 1<<31-1) {
		return
	}
	e.code = appendBaseOffsetMemory(e.code, 0x89, value, base, disp)
}

func appendBaseOffsetMemory(code []byte, opcode byte, data, base backend.Register, disp uint64) []byte {
	if disp == 0 {
		return appendMemoryReg(code, opcode, uint8(data), uint8(base))
	}
	code = appendREX(code, uint8(data), uint8(base))
	mode := byte(0x80) // signed 32-bit displacement
	if int64(disp) >= -128 && int64(disp) <= 127 {
		mode = 0x40 // signed 8-bit displacement
	}
	code = append(code, opcode, mode|byte(data&7)<<3|byte(base&7))
	if base&7 == 4 {
		code = append(code, 0x24) // SIB: no index, base RSP or R12.
	}
	if mode == 0x40 {
		return append(code, byte(disp))
	}
	return appendDisp32(code, uint32(disp))
}

func appendIndexedMemory(code []byte, opcode byte, data, base, index backend.Register, scale, disp uint64) []byte {
	var scaleBits byte
	switch scale {
	case 2:
		scaleBits = 1
	case 4:
		scaleBits = 2
	case 8:
		scaleBits = 3
	}
	rex := byte(0x48)
	if data&8 != 0 {
		rex |= 4
	}
	if index&8 != 0 {
		rex |= 2
	}
	if base&8 != 0 {
		rex |= 1
	}
	mode := byte(0x80) // signed 32-bit displacement
	if disp == 0 && base&7 != 5 {
		mode = 0
	} else if int64(disp) >= -128 && int64(disp) <= 127 {
		mode = 0x40 // signed 8-bit displacement
	}
	code = append(code, rex, opcode, mode|byte(data&7)<<3|4, scaleBits<<6|byte(index&7)<<3|byte(base&7))
	switch mode {
	case 0:
		return code
	case 0x40:
		return append(code, byte(disp))
	default:
		return appendDisp32(code, uint32(disp))
	}
}

func (e *ByteAssembler) Syscall() {
	e.code = append(e.code, 0x0F, 0x05)
}

func (e *ByteAssembler) CallC() {
	e.code = append(e.code, 0x31, 0xC0)       // AL = 0 vector arguments for SysV varargs.
	e.code = append(e.code, 0x41, 0xFF, 0xD3) // CALL R11
}
func (e *ByteAssembler) CallGo() {
	e.code = append(e.code, 0x41, 0xFF, 0xD3) // CALL R11
}
func (e *ByteAssembler) SetPC(pc int) {
	if !e.require(pc >= 0 && pc <= int(^uint(0)>>1)-32 && pc%1 == 0) {
		return
	}
	e.pc = pc
}
func (e *ByteAssembler) AssembledSize() int { return len(e.code) }

// Bytes returns encoded bytes, valid until the next assembler mutation.
// Treat the result as read-only; copy it to retain or modify it.
func (e *ByteAssembler) Err() error { return e.err }
func (e *ByteAssembler) Bytes() []byte {
	if e.err != nil {
		return nil
	}
	return e.code
}

// Relocations returns read-only borrowed entries valid until the next assembler
// mutation. Copy the slice to retain or modify it.
func (e *ByteAssembler) Relocations() []machine.Relocation { return e.relocations }
func (*ByteAssembler) SaveLink(int)                        {}
func (*ByteAssembler) RestoreLink(int)                     {}

func appendMemoryReg(code []byte, opcode, dataReg, baseReg uint8) []byte {
	code = appendREX(code, dataReg, baseReg)
	base := baseReg & 7
	switch base {
	case 4: // RSP or R12 needs a SIB byte.
		return append(code, opcode, (dataReg&7)<<3|4, 0x24)
	case 5: // RBP or R13 cannot use the mod=00 base encoding.
		return append(code, opcode, 0x40|(dataReg&7)<<3|5, 0)
	default:
		return append(code, opcode, (dataReg&7)<<3|base)
	}
}

func (e *ByteAssembler) Spill(src backend.Register, slot int) {
	if !e.require((validGeneralRegisters(src)) && (validSlot(slot, e.goHomes))) {
		return
	}

	if e.stack == abi.StackPlatform {
		e.code = appendStackStore(e.code, uint8(src), (slot+e.goHomes)*8)
		return
	}
	e.code = appendREX(e.code, uint8(src), 10)
	e.code = append(e.code, 0x89, 0x82|byte(src&7)<<3)
	e.code = appendDisp32(e.code, uint32(slot*8))
}

func (e *ByteAssembler) Reload(dst backend.Register, slot int) {
	if !e.require((validGeneralRegisters(dst)) && (validSlot(slot, e.goHomes))) {
		return
	}

	if e.stack == abi.StackPlatform {
		e.code = appendStackLoad(e.code, uint8(dst), (slot+e.goHomes)*8)
		return
	}
	e.code = appendREX(e.code, uint8(dst), 10)
	e.code = append(e.code, 0x8B, 0x82|byte(dst&7)<<3)
	e.code = appendDisp32(e.code, uint32(slot*8))
}

func (e *ByteAssembler) FrameEnter(frame int) {
	if !e.require((frame >= 0 && uint64(frame) <= 1<<20)) {
		return
	}

	e.code = append(e.code, 0x48, 0x81, 0xEC)
	e.code = appendDisp32(e.code, uint32(frame))
}
func (e *ByteAssembler) FrameLeave(frame int) {
	if !e.require((frame >= 0 && uint64(frame) <= 1<<20)) {
		return
	}

	e.code = append(e.code, 0x48, 0x81, 0xC4)
	e.code = appendDisp32(e.code, uint32(frame))
}
func (e *ByteAssembler) Ret() { e.code = append(e.code, 0xC3) }
func (e *ByteAssembler) CompareImmediate(reg backend.Register, value int64) {
	if !e.require((validGeneralRegisters(reg))) {
		return
	}

	if !e.require(reg >= 0 && reg <= 15 && value >= -1<<31 && value <= 1<<31-1) {
		return
	}
	e.code = append(e.code, 0x48|byte(reg>>3), 0x81, 0xF8|byte(reg&7))
	e.code = appendDisp32(e.code, uint32(value))
}
func (e *ByteAssembler) CompareRegister(left, right backend.Register) {
	if !e.require((validGeneralRegisters(left, right))) {
		return
	}

	e.code = append(e.code, 0x48|byte(right>>3)<<2|byte(left>>3), 0x39, 0xC0|byte(right&7)<<3|byte(left&7))
}
func (e *ByteAssembler) Jump(target int) {
	if !e.require(e.displacement(e.pc+5, target, 32, 1)) {
		return
	}
	e.code = appendDisp32(append(e.code, 0xE9), uint32(int32(target-(e.pc+5))))
}
func (e *ByteAssembler) JumpIf(condition op.BranchCondition, target int) {
	if !e.require((condition < 12)) {
		return
	}

	if !e.require(condition <= 11 && e.displacement(e.pc+6, target, 32, 1)) {
		return
	}
	opcode := [...]byte{0x84, 0x85, 0x8C, 0x8D, 0x8E, 0x8F, 0x82, 0x83, 0x86, 0x87, 0x88, 0x89}[condition]
	e.code = appendDisp32(append(e.code, 0x0F, opcode), uint32(int32(target-(e.pc+6))))
}

func (e *ByteAssembler) BranchZero(reg backend.Register, width uint8, nonzero bool, target int, long bool) {
	if !e.require(reg == 1 && width == 64 && !nonzero) {
		return
	}

	if !e.require((!long && e.displacement(e.pc+2, target, 8, 1)) || (long && e.displacement(e.pc+12, target, 32, 1))) {
		return
	}
	if !long {
		e.code = append(e.code, 0xE3, byte(int8(target-(e.pc+2)))) // JRCXZ
		return
	}
	// The second jump skips the far taken edge when RCX is nonzero.
	e.code = append(e.code, 0xE3, 0x05, 0xE9, 0x05, 0, 0, 0, 0xE9)
	e.code = appendDisp32(e.code, uint32(int32(target-(e.pc+12))))
}

func (e *ByteAssembler) Loop(target int, long bool) {
	if !e.require((!long && e.displacement(e.pc+2, target, 8, 1)) || (long && e.displacement(e.pc+11, target, 32, 1))) {
		return
	}
	if !long {
		e.code = append(e.code, 0xE2, byte(int8(target-(e.pc+2))))
		return
	}
	// LEA decrements RCX without modifying flags. JRCXZ skips the far jump.
	e.code = append(e.code, 0x48, 0x8D, 0x49, 0xFF, 0xE3, 0x05, 0xE9)
	e.code = appendDisp32(e.code, uint32(int32(target-(e.pc+11))))
}

func appendStackStore(code []byte, reg uint8, offset int) []byte {
	code = append(code, 0x48|((reg>>3)&1)<<2, 0x89, 0x84|(reg&7)<<3, 0x24)
	return appendDisp32(code, uint32(offset))
}

func appendStackLoad(code []byte, reg uint8, offset int) []byte {
	code = append(code, 0x48|((reg>>3)&1)<<2, 0x8B, 0x84|(reg&7)<<3, 0x24)
	return appendDisp32(code, uint32(offset))
}

func appendDisp32(code []byte, disp uint32) []byte {
	return append(code, byte(disp), byte(disp>>8), byte(disp>>16), byte(disp>>24))
}

func appendMovImm64(code []byte, rd uint8, value uint64) []byte {
	if value <= 0xffffffff {
		if rd >= 8 {
			code = append(code, 0x41)
		}
		return appendDisp32(append(code, 0xB8|(rd&7)), uint32(value))
	}
	if int64(value) >= -1<<31 && int64(value) <= 1<<31-1 {
		return appendDisp32(append(code, 0x48|(rd>>3), 0xC7, 0xC0|(rd&7)), uint32(value))
	}
	code = append(code, 0x48|(rd>>3), 0xB8|(rd&7))
	for i := 0; i < 8; i++ {
		code = append(code, byte(value>>(8*i)))
	}
	return code
}

func appendBinary(code []byte, operation op.Opcode, rd, rn, rm uint8) []byte {
	code = appendMovReg(code, rd, rn)
	if operation == op.OpMul {
		code = appendREX(code, rd, rm)
		code = append(code, 0x0F, 0xAF, 0xC0|(rd&7)<<3|(rm&7)) // IMUL rd, rm
		return code
	}
	var opcode byte
	switch operation {
	case op.OpAdd:
		opcode = 0x01
	case op.OpSub:
		opcode = 0x29
	case op.OpAnd:
		opcode = 0x21
	case op.OpOr:
		opcode = 0x09
	case op.OpXor:
		opcode = 0x31
	}
	code = appendREX(code, rm, rd)
	code = append(code, opcode, 0xC0|(rm&7)<<3|(rd&7)) // op rd, rm
	return code
}

func appendBinaryNarrow(code []byte, operation op.Opcode, width uint8, rd, rn, rm uint8) []byte {
	code = appendMovReg(code, rd, rn)
	if width == 16 {
		code = append(code, 0x66)
	}
	rex := byte(0x40) | byte(rm>>3)<<2 | byte(rd>>3)
	if operation == op.OpMul {
		rex = byte(0x40) | byte(rd>>3)<<2 | byte(rm>>3)
		return append(code, rex, 0x0F, 0xAF, 0xC0|byte(rd&7)<<3|byte(rm&7))
	}
	var opcode byte
	switch operation {
	case op.OpAdd:
		opcode = 0x01
	case op.OpSub:
		opcode = 0x29
	case op.OpAnd:
		opcode = 0x21
	case op.OpOr:
		opcode = 0x09
	case op.OpXor:
		opcode = 0x31
	}
	if width == 8 {
		opcode--
	}
	return append(code, rex, opcode, 0xC0|byte(rm&7)<<3|byte(rd&7))
}

func appendMovReg(code []byte, rd, rn uint8) []byte {
	if rd == rn {
		return code
	}
	code = appendREX(code, rn, rd)
	return append(code, 0x89, 0xC0|(rn&7)<<3|(rd&7)) // MOV rd, rn
}

func appendTestReg(code []byte, reg uint8) []byte {
	code = appendREX(code, reg, reg)
	return append(code, 0x85, 0xC0|(reg&7)<<3|(reg&7)) // TEST reg, reg
}

func appendCMovNE(code []byte, rd, rn uint8) []byte {
	code = appendREX(code, rd, rn)
	return append(code, 0x0F, 0x45, 0xC0|(rd&7)<<3|(rn&7)) // CMOVNE rd, rn
}

func appendREX(code []byte, reg, rm uint8) []byte {
	rex := byte(0x48)
	if reg&8 != 0 {
		rex |= 0x04
	}
	if rm&8 != 0 {
		rex |= 0x01
	}
	return append(code, rex)
}

// WithCPU selects an immutable contract for amd64 (or the zero baseline).
// It panics if p was resolved for another architecture.
func (e *ByteAssembler) WithCPU(p abi.ResolvedCPU) *ByteAssembler {
	if p.Architecture() != "" && p.Architecture() != "amd64" {
		panic("amd64: foreign CPU architecture")
	}
	e.profile = p
	return e
}
