package arm64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"math/bits"
)

func NewByteAssembler(stack abi.StackPractice, goHomes int) *ByteAssembler {
	return &ByteAssembler{stack: stack, goHomes: goHomes}
}

type ByteAssembler struct {
	words       []uint32
	relocations []machine.Relocation
	stack       abi.StackPractice
	goHomes     int
	pc          int
}

func (e *ByteAssembler) Constant(dst machine.Register, value uint64) {
	e.words = append(e.words, movImm64(uint32(dst), value)...)
}
func (e *ByteAssembler) Symbol(dst machine.Register, name string) {
	e.relocations = append(e.relocations, machine.Relocation{Symbol: name, Offset: len(e.words) * 4, Encoding: machine.RelocARM64WideMoves})
	e.words = append(e.words,
		0xD2800000|uint32(dst),
		0xF2800000|1<<21|uint32(dst),
		0xF2800000|2<<21|uint32(dst),
		0xF2800000|3<<21|uint32(dst),
	)
}

func (e *ByteAssembler) Move(dst, src machine.Register) {
	if dst != src {
		e.words = append(e.words, 0xAA0003E0|uint32(src)<<16|uint32(dst))
	}
}

func (e *ByteAssembler) Binary(operation ir.Opcode, width uint8, dst, left, right machine.Register) {
	word := binaryWord(operation, width, uint32(dst), uint32(left), uint32(right))
	if operation == ir.OpAdd || operation == ir.OpSub || operation == ir.OpAnd {
		word |= 1 << 29
		if operation == ir.OpAnd {
			word |= 1 << 30
		}
	}
	e.words = append(e.words, word)
}
func (e *ByteAssembler) BinaryImmediate(operation ir.Opcode, width uint8, dst, left machine.Register, value uint64) {
	if operation == ir.OpAnd || operation == ir.OpOr || operation == ir.OpXor {
		encoding, dataWidth, ok := logicalImmediatePlan(width, value)
		if !ok {
			panic("arm64: invalid logical immediate")
		}
		base := map[ir.Opcode]uint32{ir.OpAnd: 0x92000000, ir.OpOr: 0xB2000000, ir.OpXor: 0xD2000000}[operation]
		if dataWidth == 32 {
			base &^= 1 << 31
		}
		e.words = append(e.words, base|func() uint32 {
			if operation == ir.OpAnd {
				return 3 << 29
			}
			return 0
		}()|encoding|uint32(left)<<5|uint32(dst))
		return
	}
	effective, immediate, shift, ok := addSubImmediate(operation, width, value)
	if !ok {
		panic("arm64: invalid arithmetic immediate")
	}
	base := uint32(0x91000000)
	if effective == ir.OpSub {
		base = 0xD1000000
	}
	if width < 64 {
		base &^= 1 << 31
	}
	e.words = append(e.words, base|1<<29|(shift/12)<<22|immediate<<10|uint32(left)<<5|uint32(dst))
}
func (e *ByteAssembler) Narrow(dst, src machine.Register, width uint8) {
	e.words = append(e.words, 0xD3400000|uint32(width-1)<<10|uint32(src)<<5|uint32(dst))
}
func (e *ByteAssembler) SignExtend(dst, src machine.Register, width uint8) {
	e.words = append(e.words, 0x93400000|uint32(width-1)<<10|uint32(src)<<5|uint32(dst))
}
func (e *ByteAssembler) Decrement(dst, src machine.Register) {
	e.words = append(e.words, 0xF1000400|uint32(src)<<5|uint32(dst))
}
func (e *ByteAssembler) Shift(operation ir.Opcode, width uint8, dst, src machine.Register, count uint8) {
	base, registerWidth := uint32(0xD3400000), uint32(64)
	if width < 64 {
		base, registerWidth = 0x53000000, 32
	}
	immr, imms := uint32(count), registerWidth-1
	switch operation {
	case ir.OpShiftLeft:
		immr = (registerWidth - uint32(count)) % registerWidth
		imms = registerWidth - 1 - uint32(count)
	case ir.OpShiftRight:
		// LSR is the UBFM alias with all source bits available.
	case ir.OpShiftRightArithmetic:
		base, imms = 0x93400000, uint32(width-1)
		if width == 32 {
			base = 0x13000000
		}
	case ir.OpRotateLeft, ir.OpRotateRight:
		if operation == ir.OpRotateLeft {
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

func (e *ByteAssembler) Scaled(dst, base, index machine.Register, scale, _ uint64) {
	shift := uint32(bits.TrailingZeros64(scale))
	e.words = append(e.words, 0x8B000000|uint32(index)<<16|shift<<10|uint32(base)<<5|uint32(dst))
}

func (e *ByteAssembler) MulAdd(dst, base, index, factor machine.Register) {
	e.words = append(e.words, 0x9B000000|uint32(factor)<<16|uint32(base)<<10|uint32(index)<<5|uint32(dst))
}

func (e *ByteAssembler) SelectValue(dst, condition, ifTrue, ifFalse machine.Register) {
	e.words = append(e.words, 0xF100001F|uint32(condition)<<5) // CMP condition, #0
	e.words = append(e.words, 0x9A800000|uint32(ifTrue)<<5|uint32(ifFalse)<<16|1<<12|uint32(dst))
}

func (e *ByteAssembler) SelectFlags(dst, ifTrue, ifFalse machine.Register, condition ir.BranchCondition) {
	code := [...]uint32{0, 1, 11, 10, 13, 12, 3, 2, 9, 8, 4, 5}[condition]
	e.words = append(e.words, 0x9A800000|uint32(ifFalse)<<16|code<<12|uint32(ifTrue)<<5|uint32(dst))
}

func (e *ByteAssembler) Load64(dst, address machine.Register) {
	e.words = append(e.words, 0xF9400000|uint32(address)<<5|uint32(dst))
}

func (e *ByteAssembler) Store64(address, value machine.Register) {
	e.words = append(e.words, 0xF9000000|uint32(address)<<5|uint32(value))
}

func (e *ByteAssembler) Load8(dst, address machine.Register) {
	e.words = append(e.words, 0x39400000|uint32(address)<<5|uint32(dst))
}

func (e *ByteAssembler) Store8(address, value machine.Register) {
	e.words = append(e.words, 0x39000000|uint32(address)<<5|uint32(value))
}

func (e *ByteAssembler) LoadAddress(dst, base, index machine.Register, scale, _ uint64) {
	shift := uint32(0)
	if scale == 8 {
		shift = 1 << 12
	}
	e.words = append(e.words, 0xF8606800|uint32(index)<<16|shift|uint32(base)<<5|uint32(dst))
}

func (e *ByteAssembler) StoreAddress(base, index, value machine.Register, scale, _ uint64) {
	shift := uint32(0)
	if scale == 8 {
		shift = 1 << 12
	}
	e.words = append(e.words, 0xF8206800|uint32(index)<<16|shift|uint32(base)<<5|uint32(value))
}

func (e *ByteAssembler) LoadOffset(dst, base machine.Register, disp uint64) {
	e.words = append(e.words, arm64BaseOffsetWord(0xF9400000, 0xF8400000, dst, base, disp))
}

func (e *ByteAssembler) StoreOffset(base, value machine.Register, disp uint64) {
	e.words = append(e.words, arm64BaseOffsetWord(0xF9000000, 0xF8000000, value, base, disp))
}

func arm64BaseOffsetWord(scaledOpcode, unscaledOpcode uint32, data, base machine.Register, disp uint64) uint32 {
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
func (e *ByteAssembler) CallGo()                           { e.CallC() }
func (e *ByteAssembler) SetPC(pc int)                      { e.pc = pc }
func (e *ByteAssembler) AssembledSize() int                { return len(e.words) * 4 }
func (e *ByteAssembler) Bytes() []byte                     { return encodeWords(e.words) }
func (e *ByteAssembler) Relocations() []machine.Relocation { return e.relocations }
func (e *ByteAssembler) SaveLink(slot int) {
	if e.stack == abi.StackPlatform {
		e.words = append(e.words, 0xF90003FE) // STR X30, [SP]
		return
	}
	e.Spill(30, slot)
}
func (e *ByteAssembler) RestoreLink(slot int) {
	if e.stack == abi.StackPlatform {
		e.words = append(e.words, 0xF94003FE) // LDR X30, [SP]
		return
	}
	e.Reload(30, slot)
}

func (e *ByteAssembler) Spill(src machine.Register, slot int) {
	base := uint32(8)
	if e.stack == abi.StackPlatform {
		base = 31
		slot += 1 + e.goHomes // LR at 0(SP), Go call homes above it.
	}
	e.words = append(e.words, 0xF9000000|uint32(slot)<<10|base<<5|uint32(src))
}

func (e *ByteAssembler) Reload(dst machine.Register, slot int) {
	base := uint32(8)
	if e.stack == abi.StackPlatform {
		base = 31
		slot += 1 + e.goHomes // LR at 0(SP), Go call homes above it.
	}
	e.words = append(e.words, 0xF9400000|uint32(slot)<<10|base<<5|uint32(dst))
}

// SaveCFloat preserves the AAPCS64 callee-saved low half of D8-D15.
func (e *ByteAssembler) SaveCFloat(reg machine.Register, slot int) {
	slot += 1 + e.goHomes
	e.words = append(e.words, 0xFD000000|uint32(slot)<<10|31<<5|uint32(reg))
}

func (e *ByteAssembler) RestoreCFloat(reg machine.Register, slot int) {
	slot += 1 + e.goHomes
	e.words = append(e.words, 0xFD400000|uint32(slot)<<10|31<<5|uint32(reg))
}

func (e *ByteAssembler) FrameEnter(frame int) {
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
	e.words = append(e.words, 0xF85F83FD) // LDUR X29, [SP, #-8]
	if frame <= 240 {
		e.words = append(e.words, 0xF8400400|uint32(frame)<<12|31<<5|30) // LDR X30, [SP], #frame
		return
	}
	e.words = append(e.words, 0xF94003FE) // LDR X30, [SP]
	e.words = appendSPAdjust(e.words, frame, false)
}
func (e *ByteAssembler) Ret() { e.words = append(e.words, 0xD65F03C0) }
func (e *ByteAssembler) CompareImmediate(reg machine.Register, value int64) {
	base := uint32(0xF100001F)
	if value < 0 {
		base = 0xB100001F
		value = -value
	}
	e.words = append(e.words, base|uint32(value)<<10|uint32(reg)<<5)
}
func (e *ByteAssembler) CompareRegister(left, right machine.Register) {
	e.words = append(e.words, 0xEB00001F|uint32(right)<<16|uint32(left)<<5)
}
func (e *ByteAssembler) Jump(target int) {
	distance := (target - e.pc) / 4
	e.words = append(e.words, 0x14000000|uint32(distance)&0x3ffffff)
}
func (e *ByteAssembler) JumpIf(condition ir.BranchCondition, target int) {
	code := [...]uint32{0, 1, 11, 10, 13, 12, 3, 2, 9, 8, 4, 5}[condition]
	distance := (target - e.pc) / 4
	e.words = append(e.words, 0x54000000|(uint32(distance)&0x7ffff)<<5|code)
}

func (e *ByteAssembler) BranchZero(reg machine.Register, width uint8, nonzero bool, target int, long bool) {
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

func (e *ByteAssembler) BranchBit(reg machine.Register, _ uint8, bit uint8, nonzero bool, target int, long bool) {
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

func binaryWord(operation ir.Opcode, width uint8, rd, rn, rm uint32) uint32 {
	var base uint32
	switch operation {
	case ir.OpAdd:
		base = 0x8B000000
	case ir.OpSub:
		base = 0xCB000000
	case ir.OpMul:
		base = 0x9B007C00
	case ir.OpAnd:
		base = 0x8A000000
	case ir.OpOr:
		base = 0xAA000000
	case ir.OpXor:
		base = 0xCA000000
	}
	if width != 0 && width < 64 {
		base &^= 1 << 31
	}
	return base | rm<<16 | rn<<5 | rd
}

func movImm64(rd uint32, value uint64) []uint32 {
	plan := ImmediatePlan(value)
	words := make([]uint32, 0, len(plan))
	for _, part := range plan {
		base := uint32(0xF2800000) // MOVK
		switch part.Op {
		case "movz":
			base = 0xD2800000
		case "movn":
			base = 0x92800000
		}
		words = append(words, base|uint32(part.Shift/16)<<21|uint32(part.Imm)<<5|rd)
	}
	return words
}

func encodeWords(words []uint32) []byte {
	code := make([]byte, len(words)*4)
	for i, word := range words {
		code[i*4+0] = byte(word)
		code[i*4+1] = byte(word >> 8)
		code[i*4+2] = byte(word >> 16)
		code[i*4+3] = byte(word >> 24)
	}
	return code
}

// DecrementNoFlags is selected only after flag liveness proves NZCV dead.
func (e *ByteAssembler) DecrementNoFlags(d, s machine.Register) {
	e.words = append(e.words, 0xd1000400|uint32(s)<<5|uint32(d))
}
