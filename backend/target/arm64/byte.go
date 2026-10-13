package arm64

import (
	"encoding/binary"
	"math/bits"

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
	words       []uint32
	relocations []machine.Relocation
	stack       abi.StackPractice
	goHomes     int
	pc          int
	err         error
}

func (e *ByteAssembler) Constant(dst backend.Register, value uint64) {
	if !e.require((validGeneralRegisters(dst))) {
		return
	}

	// Appending in place writes only the length unless the slice grows.
	w, n := immediateWords(uint32(dst), value)
	for i := range n {
		e.words = append(e.words, w[i])
	}
}
func (e *ByteAssembler) Symbol(dst backend.Register, name string) {
	if !e.require((validGeneralRegisters(dst))) {
		return
	}

	e.relocations = append(e.relocations, machine.Relocation{Symbol: name, Offset: len(e.words) * 4, Encoding: machine.RelocARM64WideMoves})
	e.words = append(e.words,
		0xD2800000|uint32(dst),
		0xF2800000|1<<21|uint32(dst),
		0xF2800000|2<<21|uint32(dst),
		0xF2800000|3<<21|uint32(dst),
	)
}

func (e *ByteAssembler) Move(dst, src backend.Register) {
	if !e.require((validGeneralRegisters(dst, src))) {
		return
	}

	if dst != src {
		e.words = append(e.words, 0xAA0003E0|uint32(src)<<16|uint32(dst))
	}
}

func (e *ByteAssembler) Binary(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	e.binary(operation, width, dst, left, right, true)
}

// BinaryNoFlags is selected only after flag liveness proves NZCV dead: it
// encodes ADD, SUB and AND without the S bit.
func (e *ByteAssembler) BinaryNoFlags(operation op.Opcode, width uint8, dst, left, right backend.Register) {
	e.binary(operation, width, dst, left, right, false)
}

func (e *ByteAssembler) binary(operation op.Opcode, width uint8, dst, left, right backend.Register, setFlags bool) {
	if !e.require((validGeneralRegisters(dst, left, right)) && (width == 0 || validWidth(width)) && (validBinary(operation))) {
		return
	}

	word := binaryWord(operation, width, uint32(dst), uint32(left), uint32(right))
	if setFlags {
		word |= binaryFlagBits(operation)
	}
	e.words = append(e.words, word)
}

// binaryFlagBits turns ADD, SUB and AND (shifted register or immediate)
// into ADDS, SUBS and ANDS.
func binaryFlagBits(operation op.Opcode) uint32 {
	switch operation {
	case op.OpAdd, op.OpSub:
		return 1 << 29
	case op.OpAnd:
		return 3 << 29
	}
	return 0
}

func (e *ByteAssembler) BinaryImmediate(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	e.binaryImmediate(operation, width, dst, left, value, true)
}

// BinaryImmediateNoFlags is selected only after flag liveness proves NZCV
// dead. Without the S bit, register 31 names SP as the destination of ADD,
// SUB and AND, so a flag-free write to XZR has no encoding.
func (e *ByteAssembler) BinaryImmediateNoFlags(operation op.Opcode, width uint8, dst, left backend.Register, value uint64) {
	if !e.require(dst != 31 || binaryFlagBits(operation) == 0) {
		return
	}
	e.binaryImmediate(operation, width, dst, left, value, false)
}

func (e *ByteAssembler) binaryImmediate(operation op.Opcode, width uint8, dst, left backend.Register, value uint64, setFlags bool) {
	if !e.require(operation != op.OpMul) {
		return
	}
	if !e.require((validGeneralRegisters(dst, left)) && (width == 0 || validWidth(width)) && (validBinary(operation))) {
		return
	}

	var base uint32
	switch operation {
	case op.OpAnd:
		base = 0x92000000
	case op.OpOr:
		base = 0xB2000000
	case op.OpXor:
		base = 0xD2000000
	}
	if base != 0 {
		encoding, dataWidth, ok := logicalImmediatePlan(width, value)
		if !ok {
			e.err = machine.ErrInvalidInstruction
			return
		}
		if dataWidth == 32 {
			base &^= 1 << 31
		}
		if setFlags {
			base |= binaryFlagBits(operation)
		}
		e.words = append(e.words, base|encoding|uint32(left)<<5|uint32(dst))
		return
	}
	effective, immediate, shift, ok := addSubImmediate(operation, width, value)
	if !ok {
		e.err = machine.ErrInvalidInstruction
		return
	}
	base = uint32(0x91000000)
	if effective == op.OpSub {
		base = 0xD1000000
	}
	if width < 64 {
		base &^= 1 << 31
	}
	if setFlags {
		base |= 1 << 29
	}
	e.words = append(e.words, base|(shift/12)<<22|immediate<<10|uint32(left)<<5|uint32(dst))
}
func (e *ByteAssembler) Narrow(dst, src backend.Register, width uint8) {
	if !e.require((validGeneralRegisters(dst, src)) && (width >= 1 && width <= 64)) {
		return
	}

	if !e.require(width >= 1 && width <= 64 && dst >= 0 && dst <= 31 && src >= 0 && src <= 31) {
		return
	}
	e.words = append(e.words, 0xD3400000|uint32(width-1)<<10|uint32(src)<<5|uint32(dst))
}
func (e *ByteAssembler) SignExtend(dst, src backend.Register, width uint8) {
	if !e.require((validGeneralRegisters(dst, src)) && (width >= 1 && width <= 64)) {
		return
	}

	if !e.require(width >= 1 && width <= 64 && dst >= 0 && dst <= 31 && src >= 0 && src <= 31) {
		return
	}
	e.words = append(e.words, 0x93400000|uint32(width-1)<<10|uint32(src)<<5|uint32(dst))
}
func (e *ByteAssembler) Decrement(dst, src backend.Register) {
	if !e.require((validGeneralRegisters(dst, src))) {
		return
	}

	e.words = append(e.words, 0xF1000400|uint32(src)<<5|uint32(dst))
}
func (e *ByteAssembler) Shift(operation op.Opcode, width uint8, dst, src backend.Register, count uint8) {
	if !e.require((validGeneralRegisters(dst, src)) && (validWidth(width)) && (count < width) && (validShift(operation))) {
		return
	}

	if !e.require((width == 8 || width == 16 || width == 32 || width == 64) && count < width) {
		return
	}
	base, registerWidth := uint32(0xD3400000), uint32(64)
	if width < 64 {
		base, registerWidth = 0x53000000, 32
	}
	immr, imms := uint32(count), registerWidth-1
	switch operation {
	case op.OpShiftLeft:
		immr = (registerWidth - uint32(count)) % registerWidth
		imms = registerWidth - 1 - uint32(count)
	case op.OpShiftRight:
		// LSR is the UBFM alias with all source bits available.
	case op.OpShiftRightArithmetic:
		base, imms = 0x93400000, uint32(width-1)
		if width == 32 {
			base = 0x13000000
		}
	case op.OpRotateLeft, op.OpRotateRight:
		if operation == op.OpRotateLeft {
			immr = (registerWidth - uint32(count)) % registerWidth
		}
		base = 0x93C00000
		if width == 32 {
			base = 0x13800000
		}
		e.words = append(e.words, base|uint32(src)<<16|immr<<10|uint32(src)<<5|uint32(dst))
		return
	}
	e.words = append(e.words, base|immr<<16|imms<<10|uint32(src)<<5|uint32(dst))
}

func (e *ByteAssembler) Scaled(dst, base, index backend.Register, scale, disp uint64) {
	if !e.require((disp == 0) && (validGeneralRegisters(dst, base, index)) && (scale != 0 && (scale&(scale-1)) == 0)) {
		return
	}

	shift := uint32(bits.TrailingZeros64(scale))
	e.words = append(e.words, 0x8B000000|uint32(index)<<16|shift<<10|uint32(base)<<5|uint32(dst))
}

func (e *ByteAssembler) MulAdd(dst, base, index, factor backend.Register) {
	if !e.require((validGeneralRegisters(dst, base, index, factor))) {
		return
	}

	e.words = append(e.words, 0x9B000000|uint32(factor)<<16|uint32(base)<<10|uint32(index)<<5|uint32(dst))
}

func (e *ByteAssembler) SelectValue(dst, condition, ifTrue, ifFalse backend.Register) {
	if !e.require((validGeneralRegisters(dst, condition, ifTrue, ifFalse))) {
		return
	}

	e.words = append(e.words, 0xF100001F|uint32(condition)<<5) // CMP condition, #0
	e.words = append(e.words, 0x9A800000|uint32(ifTrue)<<5|uint32(ifFalse)<<16|1<<12|uint32(dst))
}

func (e *ByteAssembler) SelectFlags(dst, ifTrue, ifFalse backend.Register, condition op.BranchCondition) {
	if !e.require((validGeneralRegisters(dst, ifTrue, ifFalse)) && (condition < 12)) {
		return
	}

	if !e.require(condition < 12 && dst < 32 && ifTrue < 32 && ifFalse < 32) {
		return
	}

	code := [...]uint32{0, 1, 11, 10, 13, 12, 3, 2, 9, 8, 4, 5}[condition]
	e.words = append(e.words, 0x9A800000|uint32(ifFalse)<<16|code<<12|uint32(ifTrue)<<5|uint32(dst))
}

func (e *ByteAssembler) Load64(dst, address backend.Register) {
	if !e.require((validGeneralRegisters(dst, address))) {
		return
	}

	e.words = append(e.words, 0xF9400000|uint32(address)<<5|uint32(dst))
}

func (e *ByteAssembler) Store64(address, value backend.Register) {
	if !e.require((validGeneralRegisters(address, value))) {
		return
	}

	e.words = append(e.words, 0xF9000000|uint32(address)<<5|uint32(value))
}

func (e *ByteAssembler) Load8(dst, address backend.Register) {
	if !e.require((validGeneralRegisters(dst, address))) {
		return
	}

	e.words = append(e.words, 0x39400000|uint32(address)<<5|uint32(dst))
}

func (e *ByteAssembler) Store8(address, value backend.Register) {
	if !e.require((validGeneralRegisters(address, value))) {
		return
	}

	e.words = append(e.words, 0x39000000|uint32(address)<<5|uint32(value))
}

func (e *ByteAssembler) LoadAddress(dst, base, index backend.Register, scale, disp uint64) {
	if !e.require((disp == 0) && (validGeneralRegisters(dst, base, index)) && (scale == 1 || scale == 8)) {
		return
	}

	shift := uint32(0)
	if scale == 8 {
		shift = 1 << 12
	}
	e.words = append(e.words, 0xF8606800|uint32(index)<<16|shift|uint32(base)<<5|uint32(dst))
}

func (e *ByteAssembler) StoreAddress(base, index, value backend.Register, scale, disp uint64) {
	if !e.require((disp == 0) && (validGeneralRegisters(base, index, value)) && (scale == 1 || scale == 8)) {
		return
	}

	shift := uint32(0)
	if scale == 8 {
		shift = 1 << 12
	}
	e.words = append(e.words, 0xF8206800|uint32(index)<<16|shift|uint32(base)<<5|uint32(value))
}

func (e *ByteAssembler) LoadOffset(dst, base backend.Register, disp uint64) {
	if !e.require((validGeneralRegisters(dst, base))) {
		return
	}

	if !e.require(dst >= 0 && dst <= 31 && base >= 0 && base <= 31 && (int64(disp) >= -256 && int64(disp) <= 255 || disp <= 4095*8 && disp%8 == 0)) {
		return
	}
	e.words = append(e.words, arm64BaseOffsetWord(0xF9400000, 0xF8400000, dst, base, disp))
}

func (e *ByteAssembler) StoreOffset(base, value backend.Register, disp uint64) {
	if !e.require((validGeneralRegisters(base, value))) {
		return
	}

	if !e.require(value >= 0 && value <= 31 && base >= 0 && base <= 31 && (int64(disp) >= -256 && int64(disp) <= 255 || disp <= 4095*8 && disp%8 == 0)) {
		return
	}
	e.words = append(e.words, arm64BaseOffsetWord(0xF9000000, 0xF8000000, value, base, disp))
}

func arm64BaseOffsetWord(scaledOpcode, unscaledOpcode uint32, data, base backend.Register, disp uint64) uint32 {
	offset := int64(disp)
	if offset >= 0 && offset <= 4095*8 && offset%8 == 0 {
		return scaledOpcode | uint32(offset/8)<<10 | uint32(base)<<5 | uint32(data)
	}
	return unscaledOpcode | (uint32(offset)&0x1FF)<<12 | uint32(base)<<5 | uint32(data)
}

func (e *ByteAssembler) Syscall() {
	e.words = append(e.words, 0xD4000001) // SVC #0
}

func (e *ByteAssembler) CallC() {
	e.words = append(e.words, 0xD63F0200) // BLR X16
}
func (e *ByteAssembler) CallGo() { e.CallC() }
func (e *ByteAssembler) SetPC(pc int) {
	if !e.require(pc >= 0 && pc <= int(^uint(0)>>1)-32 && pc%4 == 0) {
		return
	}
	e.pc = pc
}
func (e *ByteAssembler) AssembledSize() int { return len(e.words) * 4 }

// Bytes returns encoded bytes, valid until the next assembler mutation.
// Treat the result as read-only; copy it to retain or modify it.
func (e *ByteAssembler) Bytes() []byte {
	if e.err != nil {
		return nil
	}
	return encodeWords(e.words)
}

// Err returns the first encoding error. An invalid assembler yields no Bytes.
func (e *ByteAssembler) Err() error { return e.err }

func (e *ByteAssembler) validSlot(reg backend.Register, slot int) bool {
	if reg < 0 || reg > 31 || slot < 0 || slot > 4095 {
		e.err = machine.ErrInvalidInstruction
		return false
	}
	return e.err == nil
}

// Relocations returns read-only borrowed entries valid until the next assembler
// mutation. Copy the slice to retain or modify it.
func (e *ByteAssembler) Relocations() []machine.Relocation { return e.relocations }
func (e *ByteAssembler) SaveLink(slot int) {
	if !e.require((validSlot(slot, e.goHomes))) {
		return
	}

	if e.stack == abi.StackPlatform {
		e.words = append(e.words, 0xF90003FE) // STR X30, [SP]
		return
	}
	e.Spill(30, slot)
}
func (e *ByteAssembler) RestoreLink(slot int) {
	if !e.require((validSlot(slot, e.goHomes))) {
		return
	}

	if e.stack == abi.StackPlatform {
		e.words = append(e.words, 0xF94003FE) // LDR X30, [SP]
		return
	}
	e.Reload(30, slot)
}

func (e *ByteAssembler) Spill(src backend.Register, slot int) {
	if !e.require((validGeneralRegisters(src)) && (validSlot(slot, e.goHomes))) {
		return
	}

	if slot < 0 || slot > 4095 {
		e.err = machine.ErrInvalidInstruction
		return
	}
	base := uint32(8)
	if e.stack == abi.StackPlatform {
		base = 31
		slot += 1 + e.goHomes // LR at 0(SP), Go call homes above it.
	}
	if !e.validSlot(src, slot) {
		return
	}
	e.words = append(e.words, 0xF9000000|uint32(slot)<<10|base<<5|uint32(src))
}

func (e *ByteAssembler) Reload(dst backend.Register, slot int) {
	if !e.require((validGeneralRegisters(dst)) && (validSlot(slot, e.goHomes))) {
		return
	}

	if slot < 0 || slot > 4095 {
		e.err = machine.ErrInvalidInstruction
		return
	}
	base := uint32(8)
	if e.stack == abi.StackPlatform {
		base = 31
		slot += 1 + e.goHomes // LR at 0(SP), Go call homes above it.
	}
	if !e.validSlot(dst, slot) {
		return
	}
	e.words = append(e.words, 0xF9400000|uint32(slot)<<10|base<<5|uint32(dst))
}

// SaveCFloat preserves the AAPCS64 callee-saved low half of D8-D15.
func (e *ByteAssembler) SaveCFloat(reg backend.Register, slot int) {
	if !e.require((validGeneralRegisters(reg)) && (validSlot(slot, e.goHomes))) {
		return
	}

	if !e.require(slot >= 0 && slot <= 4095 && e.goHomes >= 0 && e.goHomes <= 4094) {
		return
	}
	slot += 1 + e.goHomes
	if !e.validSlot(reg, slot) {
		return
	}
	e.words = append(e.words, 0xFD000000|uint32(slot)<<10|31<<5|uint32(reg))
}

func (e *ByteAssembler) RestoreCFloat(reg backend.Register, slot int) {
	if !e.require((validGeneralRegisters(reg)) && (validSlot(slot, e.goHomes))) {
		return
	}

	if !e.require(slot >= 0 && slot <= 4095 && e.goHomes >= 0 && e.goHomes <= 4094) {
		return
	}
	slot += 1 + e.goHomes
	if !e.validSlot(reg, slot) {
		return
	}
	e.words = append(e.words, 0xFD400000|uint32(slot)<<10|31<<5|uint32(reg))
}

func (e *ByteAssembler) FrameEnter(frame int) {
	if !e.require(validFrame(frame)) {
		return
	}

	if frame <= 240 {
		// Match the Go linker: opening the frame and storing LR is atomic.
		e.words = append(e.words, 0xF8000C00|(uint32(-frame)&0x1ff)<<12|31<<5|30)
	} else {
		// Keep SP unchanged until both links have been stored. X17 is not
		// allocated to values and is caller-saved by the platform ABI.
		e.words = append(e.words, 0x910003F1) // ADD X17, SP, #0
		for amount := frame; amount > 0; {
			step := amount
			if step > 4080 {
				step = 4080
			}
			e.words = append(e.words, 0xD1000000|uint32(step)<<10|17<<5|17)
			amount -= step
		}
		e.words = append(e.words,
			0xF900023E, // STR X30, [X17]
			0xF81F823D, // STUR X29, [X17, #-8]
			0x9100023F, // ADD SP, X17, #0
		)
		e.words = append(e.words, 0xD10023FD) // SUB X29, SP, #8
		return
	}
	e.words = append(e.words,
		0xF81F83FD, // STUR X29, [SP, #-8]
		0xD10023FD, // SUB X29, SP, #8
	)
}
func (e *ByteAssembler) FrameLeave(frame int) {
	if !e.require(validFrame(frame)) {
		return
	}

	e.words = append(e.words, 0xF85F83FD) // LDUR X29, [SP, #-8]
	if frame <= 240 {
		e.words = append(e.words, 0xF8400400|uint32(frame)<<12|31<<5|30) // LDR X30, [SP], #frame
		return
	}
	e.words = append(e.words, 0xF94003FE) // LDR X30, [SP]
	e.words = appendSPAdjust(e.words, frame, false)
}
func (e *ByteAssembler) Ret() { e.words = append(e.words, 0xD65F03C0) }
func (e *ByteAssembler) CompareImmediate(reg backend.Register, value int64) {
	if !e.require((validGeneralRegisters(reg))) {
		return
	}

	if !e.require(reg >= 0 && reg <= 31 && value >= -4095 && value <= 4095) {
		return
	}
	base := uint32(0xF100001F)
	if value < 0 {
		base = 0xB100001F
		value = -value
	}
	e.words = append(e.words, base|uint32(value)<<10|uint32(reg)<<5)
}
func (e *ByteAssembler) CompareRegister(left, right backend.Register) {
	if !e.require((validGeneralRegisters(left, right))) {
		return
	}

	e.words = append(e.words, 0xEB00001F|uint32(right)<<16|uint32(left)<<5)
}
func (e *ByteAssembler) Jump(target int) {
	if !e.require(e.displacement(e.pc, target, 28, 4)) {
		return
	}
	distance := (target - e.pc) / 4
	e.words = append(e.words, 0x14000000|uint32(distance)&0x3ffffff)
}
func (e *ByteAssembler) JumpIf(condition op.BranchCondition, target int) {
	if !e.require((condition < 12)) {
		return
	}

	if !e.require(condition <= 11 && e.displacement(e.pc, target, 21, 4)) {
		return
	}
	code := [...]uint32{0, 1, 11, 10, 13, 12, 3, 2, 9, 8, 4, 5}[condition]
	distance := (target - e.pc) / 4
	e.words = append(e.words, 0x54000000|(uint32(distance)&0x7ffff)<<5|code)
}

// JumpIfForm expands a distant conditional into an inverted skip and B.
func (e *ByteAssembler) JumpIfForm(condition op.BranchCondition, target int, long bool) {
	if !e.require((condition < 12)) {
		return
	}

	if !e.require(condition <= 11 && (!long || e.displacement(e.pc+4, target, 28, 4))) {
		return
	}
	if !long {
		e.JumpIf(condition, target)
		return
	}
	e.JumpIf(condition^1, e.pc+8)
	distance := (target - (e.pc + 4)) / 4
	e.words = append(e.words, 0x14000000|uint32(distance)&0x3ffffff)
}

func (e *ByteAssembler) BranchZero(reg backend.Register, width uint8, nonzero bool, target int, long bool) {
	if !e.require((validGeneralRegisters(reg)) && (validWidth(width))) {
		return
	}

	if !e.require(reg >= 0 && reg <= 31 && (width == 8 || width == 16 || width == 32 || width == 64) && ((!long && e.displacement(e.pc, target, 21, 4)) || (long && e.displacement(e.pc+4, target, 28, 4)))) {
		return
	}
	base := uint32(0x34000000)
	if width == 64 {
		base |= 1 << 31
	}
	if nonzero != long {
		base |= 1 << 24
	}
	if long {
		e.words = append(e.words, base|2<<5|uint32(reg))
		distance := (target - (e.pc + 4)) / 4
		e.words = append(e.words, 0x14000000|uint32(distance)&0x3ffffff)
		return
	}
	distance := (target - e.pc) / 4
	e.words = append(e.words, base|(uint32(distance)&0x7ffff)<<5|uint32(reg))
}

func (e *ByteAssembler) BranchBit(reg backend.Register, width uint8, bit uint8, nonzero bool, target int, long bool) {
	if !e.require((validGeneralRegisters(reg)) && (validWidth(width))) {
		return
	}

	if !e.require(reg >= 0 && reg <= 31 && bit < width && ((!long && e.displacement(e.pc, target, 16, 4)) || (long && e.displacement(e.pc+4, target, 28, 4)))) {
		return
	}
	base := uint32(0x36000000) | uint32(bit>>5)<<31 | uint32(bit&31)<<19
	if nonzero != long {
		base |= 1 << 24
	}
	if long {
		e.words = append(e.words, base|2<<5|uint32(reg))
		distance := (target - (e.pc + 4)) / 4
		e.words = append(e.words, 0x14000000|uint32(distance)&0x3ffffff)
		return
	}
	distance := (target - e.pc) / 4
	e.words = append(e.words, base|(uint32(distance)&0x3fff)<<5|uint32(reg))
}

func appendSPAdjust(words []uint32, amount int, subtract bool) []uint32 {
	base := uint32(0x910003FF) // ADD SP, SP, #imm
	if subtract {
		base = 0xD10003FF // SUB SP, SP, #imm
	}
	for amount > 0 {
		step := amount
		if step > 4080 {
			step = 4080
		}
		words = append(words, base|uint32(step)<<10)
		amount -= step
	}
	return words
}

func binaryWord(operation op.Opcode, width uint8, rd, rn, rm uint32) uint32 {
	var base uint32
	switch operation {
	case op.OpAdd:
		base = 0x8B000000
	case op.OpSub:
		base = 0xCB000000
	case op.OpMul:
		base = 0x9B007C00
	case op.OpAnd:
		base = 0x8A000000
	case op.OpOr:
		base = 0xAA000000
	case op.OpXor:
		base = 0xCA000000
	}
	if width != 0 && width < 64 {
		base &^= 1 << 31
	}
	return base | rm<<16 | rn<<5 | rd
}

// appendImmediateWords encodes immediatePlan(value) into rd without
// building the plan.
func appendImmediateWords(words []uint32, rd uint32, value uint64) []uint32 {
	w, n := immediateWords(rd, value)
	return append(words, w[:n]...)
}

// immediateWords returns the MOVZ or MOVN and MOVK words that put value in
// rd, and their count.
func immediateWords(rd uint32, value uint64) (w [4]uint32, n int) {
	first, skip := uint32(0xD2800000), uint16(0) // MOVZ over the nonzero halves
	if nonzeroHalves(^value) < nonzeroHalves(value) {
		first, skip = 0x92800000, 0xffff // MOVN over the halves not all ones
	}
	for i := uint32(0); i < 4; i++ {
		half := uint16(value >> (16 * i))
		if half == skip {
			continue
		}
		if n == 0 {
			w[n] = first | i<<21 | uint32(half^skip)<<5 | rd
		} else {
			w[n] = 0xF2800000 | i<<21 | uint32(half)<<5 | rd
		}
		n++
	}
	if n == 0 {
		w[0], n = first|rd, 1
	}
	return w, n
}

func encodeWords(words []uint32) []byte {
	code := make([]byte, len(words)*4)
	for i, word := range words {
		binary.LittleEndian.PutUint32(code[i*4:], word)
	}
	return code
}

// DecrementNoFlags is selected only after flag liveness proves NZCV dead.
func (e *ByteAssembler) DecrementNoFlags(d, s backend.Register) {
	if !e.require((validGeneralRegisters(d, s))) {
		return
	}

	e.words = append(e.words, 0xd1000400|uint32(s)<<5|uint32(d))
}
