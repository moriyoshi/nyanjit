package amd64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func NewByteAssembler(stack abi.StackPractice, goHomes int) *ByteAssembler {
	return &ByteAssembler{stack: stack, goHomes: goHomes}
}

type ByteAssembler struct {
	code        []byte
	relocations []machine.Relocation
	stack       abi.StackPractice
	goHomes     int
	pc          int
	profile     abi.CPUProfile
}

func (e *ByteAssembler) Constant(dst machine.Register, value uint64) {
	e.code = appendMovImm64(e.code, uint8(dst), value)
}
func (e *ByteAssembler) Symbol(dst machine.Register, name string) {
	e.relocations = append(e.relocations, machine.Relocation{Symbol: name, Offset: len(e.code), Encoding: machine.RelocAMD64Immediate})
	e.code = append(e.code, 0x48|byte(dst>>3), 0xB8|byte(dst&7), 0, 0, 0, 0, 0, 0, 0, 0)
}

func (e *ByteAssembler) Move(dst, src machine.Register) {
	e.code = appendMovReg(e.code, uint8(dst), uint8(src))
}

func (e *ByteAssembler) Binary(operation ir.Opcode, width uint8, dst, left, right machine.Register) {
	if width == 0 || width == 64 || width == 8 && operation == ir.OpMul {
		e.code = appendBinary(e.code, operation, uint8(dst), uint8(left), uint8(right))
		return
	}
	e.code = appendBinaryNarrow(e.code, operation, width, uint8(dst), uint8(left), uint8(right))
}
func (e *ByteAssembler) BinaryImmediate(operation ir.Opcode, width uint8, dst, left machine.Register, value uint64) {
	e.Move(dst, left)
	group := map[ir.Opcode]byte{ir.OpAdd: 0, ir.OpOr: 1, ir.OpAnd: 4, ir.OpSub: 5, ir.OpXor: 6}[operation]
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
func (e *ByteAssembler) Narrow(dst, src machine.Register, width uint8) {
	rex := byte(0x40) | byte(dst>>3)<<2 | byte(src>>3)
	registers := byte(0xC0) | byte(dst&7)<<3 | byte(src&7)
	switch width {
	case 8:
		e.code = append(e.code, rex, 0x0F, 0xB6, registers)
	case 16:
		e.code = append(e.code, rex, 0x0F, 0xB7, registers)
	case 32:
		e.code = append(e.code, rex, 0x8B, registers)
	}
}
func (e *ByteAssembler) SignExtend(dst, src machine.Register, width uint8) {
	rex := byte(0x48) | byte(dst>>3)<<2 | byte(src>>3)
	registers := byte(0xC0) | byte(dst&7)<<3 | byte(src&7)
	switch width {
	case 8:
		e.code = append(e.code, rex, 0x0F, 0xBE, registers)
	case 16:
		e.code = append(e.code, rex, 0x0F, 0xBF, registers)
	case 32:
		e.code = append(e.code, rex, 0x63, registers)
	}
}
func (e *ByteAssembler) Decrement(dst, src machine.Register) {
	e.code = appendMovReg(e.code, uint8(dst), uint8(src))
	e.code = append(e.code, 0x48|byte(dst>>3), 0xFF, 0xC8|byte(dst&7))
}
func (e *ByteAssembler) Increment(dst, src machine.Register) {
	e.code = appendMovReg(e.code, uint8(dst), uint8(src))
	e.code = append(e.code, 0x48|byte(dst>>3), 0xFF, 0xC0|byte(dst&7))
}
func (e *ByteAssembler) Shift(operation ir.Opcode, width uint8, dst, src machine.Register, count uint8) {
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
	case ir.OpRotateLeft:
		group = 0
	case ir.OpRotateRight:
		group = 1
	case ir.OpShiftLeft:
		group = 4
	case ir.OpShiftRight:
		group = 5
	case ir.OpShiftRightArithmetic:
		group = 7
	}
	opcode := byte(0xC1)
	if width == 8 {
		opcode = 0xC0
	}
	e.code = append(e.code, rex, opcode, 0xC0|group<<3|byte(dst&7), count)
}

func (e *ByteAssembler) Scaled(dst, base, index machine.Register, scale, disp uint64) {
	e.code = appendIndexedMemory(e.code, 0x8D, dst, base, index, scale, disp)
}

func (e *ByteAssembler) SelectValue(dst, condition, ifTrue, ifFalse machine.Register) {
	e.code = appendMovReg(e.code, uint8(dst), uint8(ifFalse))
	e.code = appendTestReg(e.code, uint8(condition))
	e.code = appendCMovNE(e.code, uint8(dst), uint8(ifTrue))
}

func (e *ByteAssembler) Load64(dst, address machine.Register) {
	e.code = appendMemoryReg(e.code, 0x8B, uint8(dst), uint8(address))
}

func (e *ByteAssembler) Store64(address, value machine.Register) {
	e.code = appendMemoryReg(e.code, 0x89, uint8(value), uint8(address))
}

func (e *ByteAssembler) Load8(dst, address machine.Register) {
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

func (e *ByteAssembler) Store8(address, value machine.Register) {
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

func (e *ByteAssembler) LoadAddress(dst, base, index machine.Register, scale, disp uint64) {
	e.code = appendIndexedMemory(e.code, 0x8B, dst, base, index, scale, disp)
}

func (e *ByteAssembler) StoreAddress(base, index, value machine.Register, scale, disp uint64) {
	e.code = appendIndexedMemory(e.code, 0x89, value, base, index, scale, disp)
}

func (e *ByteAssembler) LoadOffset(dst, base machine.Register, disp uint64) {
	e.code = appendBaseOffsetMemory(e.code, 0x8B, dst, base, disp)
}

func (e *ByteAssembler) StoreOffset(base, value machine.Register, disp uint64) {
	e.code = appendBaseOffsetMemory(e.code, 0x89, value, base, disp)
}

func appendBaseOffsetMemory(code []byte, opcode byte, data, base machine.Register, disp uint64) []byte {
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

func appendIndexedMemory(code []byte, opcode byte, data, base, index machine.Register, scale, disp uint64) []byte {
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
func (e *ByteAssembler) SetPC(pc int)                      { e.pc = pc }
func (e *ByteAssembler) AssembledSize() int                { return len(e.code) }
func (e *ByteAssembler) Bytes() []byte                     { return e.code }
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

func (e *ByteAssembler) Spill(src machine.Register, slot int) {
	if e.stack == abi.StackPlatform {
		e.code = appendStackStore(e.code, uint8(src), (slot+e.goHomes)*8)
		return
	}
	e.code = appendREX(e.code, uint8(src), 10)
	e.code = append(e.code, 0x89, 0x82|byte(src&7)<<3)
	e.code = appendDisp32(e.code, uint32(slot*8))
}

func (e *ByteAssembler) Reload(dst machine.Register, slot int) {
	if e.stack == abi.StackPlatform {
		e.code = appendStackLoad(e.code, uint8(dst), (slot+e.goHomes)*8)
		return
	}
	e.code = appendREX(e.code, uint8(dst), 10)
	e.code = append(e.code, 0x8B, 0x82|byte(dst&7)<<3)
	e.code = appendDisp32(e.code, uint32(slot*8))
}

func (e *ByteAssembler) FrameEnter(frame int) {
	e.code = append(e.code, 0x48, 0x81, 0xEC)
	e.code = appendDisp32(e.code, uint32(frame))
}
func (e *ByteAssembler) FrameLeave(frame int) {
	e.code = append(e.code, 0x48, 0x81, 0xC4)
	e.code = appendDisp32(e.code, uint32(frame))
}
func (e *ByteAssembler) Ret() { e.code = append(e.code, 0xC3) }
func (e *ByteAssembler) CompareImmediate(reg machine.Register, value int64) {
	e.code = append(e.code, 0x48|byte(reg>>3), 0x81, 0xF8|byte(reg&7))
	e.code = appendDisp32(e.code, uint32(value))
}
func (e *ByteAssembler) CompareRegister(left, right machine.Register) {
	e.code = append(e.code, 0x48|byte(right>>3)<<2|byte(left>>3), 0x39, 0xC0|byte(right&7)<<3|byte(left&7))
}
func (e *ByteAssembler) Jump(target int) {
	e.code = appendDisp32(append(e.code, 0xE9), uint32(int32(target-(e.pc+5))))
}
func (e *ByteAssembler) JumpIf(condition ir.BranchCondition, target int) {
	opcode := [...]byte{0x84, 0x85, 0x8C, 0x8D, 0x8E, 0x8F, 0x82, 0x83, 0x86, 0x87, 0x88, 0x89}[condition]
	e.code = appendDisp32(append(e.code, 0x0F, opcode), uint32(int32(target-(e.pc+6))))
}

func (e *ByteAssembler) BranchZero(_ machine.Register, _ uint8, _ bool, target int, long bool) {
	if !long {
		e.code = append(e.code, 0xE3, byte(int8(target-(e.pc+2)))) // JRCXZ
		return
	}
	// The second jump skips the far taken edge when RCX is nonzero.
	e.code = append(e.code, 0xE3, 0x05, 0xE9, 0x05, 0, 0, 0, 0xE9)
	e.code = appendDisp32(e.code, uint32(int32(target-(e.pc+12))))
}

func (e *ByteAssembler) Loop(target int, long bool) {
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

func appendBinary(code []byte, operation ir.Opcode, rd, rn, rm uint8) []byte {
	code = appendMovReg(code, rd, rn)
	if operation == ir.OpMul {
		code = appendREX(code, rd, rm)
		code = append(code, 0x0F, 0xAF, 0xC0|(rd&7)<<3|(rm&7)) // IMUL rd, rm
		return code
	}
	var opcode byte
	switch operation {
	case ir.OpAdd:
		opcode = 0x01
	case ir.OpSub:
		opcode = 0x29
	case ir.OpAnd:
		opcode = 0x21
	case ir.OpOr:
		opcode = 0x09
	case ir.OpXor:
		opcode = 0x31
	}
	code = appendREX(code, rm, rd)
	code = append(code, opcode, 0xC0|(rm&7)<<3|(rd&7)) // op rd, rm
	return code
}

func appendBinaryNarrow(code []byte, operation ir.Opcode, width uint8, rd, rn, rm uint8) []byte {
	code = appendMovReg(code, rd, rn)
	if width == 16 {
		code = append(code, 0x66)
	}
	rex := byte(0x40) | byte(rm>>3)<<2 | byte(rd>>3)
	if operation == ir.OpMul {
		rex = byte(0x40) | byte(rd>>3)<<2 | byte(rm>>3)
		return append(code, rex, 0x0F, 0xAF, 0xC0|byte(rd&7)<<3|byte(rm&7))
	}
	var opcode byte
	switch operation {
	case ir.OpAdd:
		opcode = 0x01
	case ir.OpSub:
		opcode = 0x29
	case ir.OpAnd:
		opcode = 0x21
	case ir.OpOr:
		opcode = 0x09
	case ir.OpXor:
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

// WithCPUProfile selects validated instruction extensions for this assembler.
func (e *ByteAssembler) WithCPUProfile(p abi.CPUProfile) *ByteAssembler {
	e.profile = p.Clone()
	return e
}
