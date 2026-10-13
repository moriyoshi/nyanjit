package loong64

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"strings"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Every instruction expansion records its bytes and text together. Layout uses
// the identical visitor, including aliases, constants and long branch forms.
type concrete struct {
	word uint32
	text *plannedText
}

// Text operands are retained without formatting on the word/byte path.
// Physical sizing and clobber inspection omit them entirely.
type plannedText struct {
	format string
	args   []any
}

func (p *plannedText) String() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf(p.format, p.args...)
}

type ByteAssembler struct {
	plan        []concrete
	sizeOnly    bool
	pc          int
	stack       abi.StackPractice
	goHomes     int
	left, right backend.Register
	relocations []machine.Relocation
	err         error
}
type TextAssembler struct{ *ByteAssembler }

// NewByteAssembler configures the stack policy and Go argument-home count.
// Use zero goHomes for an unmanaged layout.
func NewByteAssembler(s abi.StackPractice, goHomes int) *ByteAssembler {
	e := &ByteAssembler{stack: s, goHomes: goHomes}
	e.require(goHomes >= 0 && goHomes <= 4094 && (s == abi.StackNoUse || s == abi.StackPlatform))
	return e
}
func NewTextAssembler(s abi.StackPractice, goHomes int) *TextAssembler {
	return &TextAssembler{NewByteAssembler(s, goHomes)}
}
func (e *ByteAssembler) SetPC(pc int) {
	if !e.require(pc >= 0 && pc <= int(^uint(0)>>1)-32 && pc%4 == 0) {
		return
	}
	e.pc = pc
}
func (e *ByteAssembler) AssembledSize() int { return len(e.plan) * 4 }

// Bytes returns encoded bytes, valid until the next assembler mutation.
// Treat the result as read-only; copy it to retain or modify it.
func (e *ByteAssembler) Bytes() []byte {
	if e.err != nil {
		return nil
	}
	b := make([]byte, e.AssembledSize())
	for n, i := range e.plan {
		binary.LittleEndian.PutUint32(b[n*4:], i.word)
	}
	return b
}

// Relocations returns read-only borrowed entries valid until the next assembler
// mutation. Copy the slice to retain or modify it.
func (e *ByteAssembler) Relocations() []machine.Relocation {
	return e.relocations
}
func (e *ByteAssembler) Text() string {
	var s strings.Builder
	for n, i := range e.plan {
		fmt.Fprintf(&s, "%04x  %s\n", n*4, i.text)
	}
	return s.String()
}
func (e *ByteAssembler) emit(w uint32, f string, a ...any) {
	var text *plannedText
	if !e.sizeOnly {
		text = &plannedText{format: f, args: a}
	}
	e.plan = append(e.plan, concrete{word: w, text: text})
}
func (e *ByteAssembler) rr(op uint32, name string, d, a backend.Register) {
	e.emit(op|uint32(a)<<5|uint32(d), "%s r%d, r%d", name, d, a)
}
func (e *ByteAssembler) rrr(op uint32, name string, d, a, b backend.Register) {
	e.emit(op|uint32(b)<<10|uint32(a)<<5|uint32(d), "%s r%d, r%d, r%d", name, d, a, b)
}
func (e *ByteAssembler) ri(op uint32, name string, d, a backend.Register, v int64) {
	min, max := int64(0), int64(4095)
	if op == 0x02c00000 || op == 0x02400000 {
		min, max = -2048, 2047
	}
	if v < min || v > max {
		e.err = fmt.Errorf("loong64 %s immediate %d outside [%d,%d]: %w", name, v, min, max, machine.ErrInvalidInstruction)
		return
	}
	e.emit(op|(uint32(v)&4095)<<10|uint32(a)<<5|uint32(d), "%s r%d, r%d, %d", name, d, a, v)
}
func (e *ByteAssembler) upper(op uint32, name string, d backend.Register, v uint64) {
	e.emit(op|uint32(v&0xfffff)<<5|uint32(d), "%s r%d, 0x%x", name, d, v&0xfffff)
}
func (e *ByteAssembler) Constant(d backend.Register, v uint64) {
	if !e.require((validGeneralRegisters(d))) {
		return
	}

	// Choose the shortest initialization of low 52 bits, then set the high 12
	// only if they differ from the sign extension produced by the lower part.
	if int64(v) >= -2048 && int64(v) <= 2047 {
		e.ri(0x02c00000, "addi.d", d, 0, int64(v))
		return
	}
	if v <= 4095 {
		e.ri(0x03800000, "ori", d, 0, int64(v))
		return
	}
	if v&((1<<52)-1) == 0 {
		e.ri(0x03000000, "lu52i.d", d, 0, int64(v>>52))
		return
	}
	low := uint64(int64(int32(v)))
	// A signed 12-bit seed can avoid lu12i.w even with unrelated high bits.
	seed := uint64(int64(v<<52) >> 52)
	if v&0xffffffff == seed&0xffffffff {
		e.ri(0x02c00000, "addi.d", d, 0, int64(seed))
		low = seed
	} else if v&0xffffffff <= 4095 {
		e.ri(0x03800000, "ori", d, 0, int64(v&4095))
		low = v & 4095
	} else {
		e.upper(0x14000000, "lu12i.w", d, v>>12)
		if v&4095 != 0 {
			e.ri(0x03800000, "ori", d, d, int64(v&4095))
		}
	}
	if low&((1<<52)-1) != v&((1<<52)-1) {
		e.upper(0x16000000, "lu32i.d", d, v>>32)
		low = uint64(int64(v<<12) >> 12)
	}
	if low != v {
		e.ri(0x03000000, "lu52i.d", d, d, int64(v>>52))
	}
}
func (e *ByteAssembler) Symbol(d backend.Register, name string) {
	if !e.require((validGeneralRegisters(d))) {
		return
	}

	e.relocations = append(e.relocations, machine.Relocation{Symbol: name, Offset: e.AssembledSize(), Encoding: machine.RelocLoong64Constant})
	e.upper(0x14000000, "lu12i.w", d, 0)
	e.ri(0x03800000, "ori", d, d, 0)
	e.upper(0x16000000, "lu32i.d", d, 0)
	e.ri(0x03000000, "lu52i.d", d, d, 0)
}
func (e *ByteAssembler) Move(d, a backend.Register) {
	if !e.require((validGeneralRegisters(d, a))) {
		return
	}

	if d != a {
		e.rrr(0x00150000, "or", d, a, 0)
	}
}
func (e *ByteAssembler) Binary(operation op.Opcode, width uint8, d, a, b backend.Register) {
	if !e.require((validGeneralRegisters(d, a, b)) && (width == 0 || validWidth(width)) && (validBinary(operation))) {
		return
	}

	switch operation {
	case op.OpAdd:
		e.rrr(0x00108000, "add.d", d, a, b)
	case op.OpSub:
		e.rrr(0x00118000, "sub.d", d, a, b)
	case op.OpMul:
		e.rrr(0x001d8000, "mul.d", d, a, b)
	case op.OpAnd:
		e.rrr(0x00148000, "and", d, a, b)
	case op.OpOr:
		e.rrr(0x00150000, "or", d, a, b)
	case op.OpXor:
		e.rrr(0x00158000, "xor", d, a, b)
	}
}
func (e *ByteAssembler) BinaryImmediate(operation op.Opcode, width uint8, d, a backend.Register, v uint64) {
	if !e.require(operation != op.OpMul) {
		return
	}
	if !e.require((validGeneralRegisters(d, a)) && (width == 0 || validWidth(width)) && (validBinary(operation))) {
		return
	}

	switch operation {
	case op.OpAdd:
		e.ri(0x02c00000, "addi.d", d, a, int64(v))
	case op.OpSub:
		e.ri(0x02c00000, "addi.d", d, a, -int64(v))
	case op.OpAnd:
		e.ri(0x03400000, "andi", d, a, int64(v))
	case op.OpOr:
		e.ri(0x03800000, "ori", d, a, int64(v))
	case op.OpXor:
		e.ri(0x03c00000, "xori", d, a, int64(v))
	}
}
func (e *ByteAssembler) Decrement(d, a backend.Register) {
	if !e.require((validGeneralRegisters(d, a))) {
		return
	}
	e.ri(0x02c00000, "addi.d", d, a, -1)
}
func (e *ByteAssembler) Increment(d, a backend.Register) {
	if !e.require((validGeneralRegisters(d, a))) {
		return
	}
	e.ri(0x02c00000, "addi.d", d, a, 1)
}
func (e *ByteAssembler) Narrow(d, a backend.Register, w uint8) {
	if !e.require((validGeneralRegisters(d, a)) && (w >= 1 && w <= 64)) {
		return
	}

	if w == 64 {
		e.Move(d, a)
		return
	}
	e.emit(0x00c00000|uint32(w-1)<<16|uint32(a)<<5|uint32(d), "bstrpick.d r%d, r%d, %d, 0", d, a, w-1)
}
func (e *ByteAssembler) SignExtend(d, a backend.Register, w uint8) {
	if !e.require((validGeneralRegisters(d, a)) && (w >= 1 && w <= 64)) {
		return
	}

	switch w {
	case 8:
		e.rr(0x5c00, "ext.w.b", d, a)
	case 16:
		e.rr(0x5800, "ext.w.h", d, a)
	case 32:
		e.emit(0x00408000|uint32(a)<<5|uint32(d), "slli.w r%d, r%d, 0", d, a)
	default:
		if w == 64 {
			e.Move(d, a)
		} else {
			e.shift(op.OpShiftLeft, d, a, 64-w)
			e.shift(op.OpShiftRightArithmetic, d, d, 64-w)
		}
	}
}
func (e *ByteAssembler) shift(operation op.Opcode, d, a backend.Register, c uint8) {
	code, name := uint32(0x00410000), "slli.d"
	if operation == op.OpShiftRight {
		code, name = 0x00450000, "srli.d"
	}
	if operation == op.OpShiftRightArithmetic {
		code, name = 0x00490000, "srai.d"
	}
	if operation == op.OpRotateRight {
		code, name = 0x004d0000, "rotri.d"
	}
	e.emit(code|uint32(c&63)<<10|uint32(a)<<5|uint32(d), "%s r%d, r%d, %d", name, d, a, c&63)
}
func (e *ByteAssembler) Shift(operation op.Opcode, w uint8, d, a backend.Register, c uint8) {
	if !e.require((validGeneralRegisters(d, a)) && (validWidth(w)) && (c < w) && (validShift(operation))) {
		return
	}

	if operation == op.OpShiftRightArithmetic && w < 64 {
		e.SignExtend(19, a, w)
		a = 19
	}
	if operation == op.OpRotateLeft || operation == op.OpRotateRight {
		if c == 0 {
			e.Move(d, a)
			return
		}
		if w >= 32 {
			if operation == op.OpRotateLeft {
				c = (w - c) % w
			}
			code := uint32(0x004d0000)
			name := "rotri.d"
			if w == 32 {
				code = 0x004c8000
				name = "rotri.w"
			}
			e.emit(code|uint32(c)<<10|uint32(a)<<5|uint32(d), "%s r%d, r%d, %d", name, d, a, c)
			return
		}
		l, r := c, w-c
		if operation == op.OpRotateRight {
			l, r = r, l
		}
		e.shift(op.OpShiftLeft, 19, a, l)
		e.shift(op.OpShiftRight, 20, a, r)
		e.Binary(op.OpOr, 64, d, 19, 20)
		return
	}
	e.shift(operation, d, a, c)
}
func (e *ByteAssembler) ShiftBy(operation op.Opcode, w uint8, d, a, c backend.Register) {
	if !e.require((validGeneralRegisters(d, a, c)) && (validWidth(w)) && (validShift(operation))) {
		return
	}

	if !e.require((Policy{}).CanShiftBy(w)) {
		return
	}

	if operation == op.OpRotateLeft || operation == op.OpRotateRight {
		if w < 32 {
			e.Move(19, a)
			for n := w; n < 32; n *= 2 {
				e.shift(op.OpShiftLeft, 20, 19, n)
				e.Binary(op.OpOr, 64, 19, 19, 20)
			}
			a = 19
		}
		if operation == op.OpRotateLeft {
			e.Binary(op.OpSub, 64, 20, 0, c)
			c = 20
		}
		code, name := uint32(0x001b8000), "rotr.d"
		if w <= 32 {
			code, name = 0x001b0000, "rotr.w"
		}
		e.rrr(code, name, d, a, c)
		if w < 64 {
			e.Narrow(d, d, w)
		}
		return
	}
	e.ri(0x02400000, "sltui", 19, c, int64(w))
	if operation == op.OpShiftRightArithmetic {
		e.ri(0x02c00000, "addi.d", 19, 19, -1)
		e.Binary(op.OpOr, 64, 19, 19, c)
		if w < 64 {
			e.SignExtend(20, a, w)
			a = 20
		}
		e.rrr(0x00198000, "sra.d", d, a, 19)
	} else {
		code, name := uint32(0x00188000), "sll.d"
		if operation == op.OpShiftRight {
			code, name = 0x00190000, "srl.d"
		}
		e.rrr(code, name, 20, a, c)
		e.rrr(0x00130000, "maskeqz", d, 20, 19)
	}
	if w < 64 {
		e.Narrow(d, d, w)
	}
}
func (e *ByteAssembler) Scaled(d, a, b backend.Register, s, disp uint64) {
	if !e.require((validGeneralRegisters(d, a, b))) {
		return
	}

	if s == 1 {
		e.Binary(op.OpAdd, 64, d, a, b)
	} else if s == 2 || s == 4 || s == 8 || s == 16 {
		n := bits.TrailingZeros64(s) - 1
		e.emit(0x002c0000|uint32(n)<<15|uint32(a)<<10|uint32(b)<<5|uint32(d), "alsl.d r%d, r%d, r%d, %d", d, b, a, n+1)
	} else {
		if s != 0 && s&(s-1) == 0 {
			e.shift(op.OpShiftLeft, 19, b, uint8(bits.TrailingZeros64(s)))
		} else {
			e.Constant(19, s)
			e.Binary(op.OpMul, 64, 19, b, 19)
		}
		e.Binary(op.OpAdd, 64, d, a, 19)
	}
	if disp != 0 {
		if int64(disp) >= -2048 && int64(disp) <= 2047 {
			e.BinaryImmediate(op.OpAdd, 64, d, d, disp)
		} else {
			e.Constant(20, disp)
			e.Binary(op.OpAdd, 64, d, d, 20)
		}
	}
}
func (e *ByteAssembler) MulAdd(d, a, b, c backend.Register) {
	if !e.require((validGeneralRegisters(d, a, b, c))) {
		return
	}

	e.Binary(op.OpMul, 64, 19, b, c)
	e.Binary(op.OpAdd, 64, d, a, 19)
}
func (e *ByteAssembler) SelectValue(d, c, a, b backend.Register) {
	if !e.require((validGeneralRegisters(d, c, a, b))) {
		return
	}

	e.rrr(0x00130000, "maskeqz", 19, a, c)
	e.rrr(0x00138000, "masknez", 20, b, c)
	e.Binary(op.OpOr, 64, d, 19, 20)
}
func (e *ByteAssembler) MulHigh(w uint8, d, a, b backend.Register) {
	if !e.require((validGeneralRegisters(d, a, b)) && (validWidth(w))) {
		return
	}

	if w == 64 {
		e.rrr(0x001e8000, "mulh.du", d, a, b)
	} else {
		e.Binary(op.OpMul, 64, d, a, b)
		e.shift(op.OpShiftRight, d, d, w)
	}
}

// MulHighSigned writes the high Bits of signed a*b to d. MULH.W reads the
// low words as signed; a narrower width shifts both operands to the top of
// r19 and r20, so MULH.D gives the product shifted left by 64-2w and its top
// w bits are the high half.
func (e *ByteAssembler) MulHighSigned(w uint8, d, a, b backend.Register) {
	if !e.require(validGeneralRegisters(d, a, b) && validWidth(w)) {
		return
	}
	switch w {
	case 64:
		e.rrr(0x001e0000, "mulh.d", d, a, b)
	case 32:
		e.rrr(0x001c8000, "mulh.w", d, a, b)
		e.Narrow(d, d, 32)
	default:
		e.shift(op.OpShiftLeft, 19, a, 64-w)
		e.shift(op.OpShiftLeft, 20, b, 64-w)
		e.rrr(0x001e0000, "mulh.d", d, 19, 20)
		e.shift(op.OpShiftRight, d, d, 64-w)
	}
}
func (e *ByteAssembler) WordCarry(i machine.Instruction) {
	if !e.require(i.Op == machine.OpCarryBit || i.Op == machine.OpWordCarry) {
		return
	}
	if !e.require((validInstructionRegisters(i))) {
		return
	}

	sub := i.Arithmetic == op.OpSub
	carry := func(d, a, b backend.Register) {
		if sub {
			e.rrr(0x00128000, "sltu", d, a, b)
		} else {
			e.Binary(op.OpAdd, 64, 20, a, b)
			e.rrr(0x00128000, "sltu", d, 20, a)
		}
	}
	if i.Op == machine.OpCarryBit {
		carry(i.A, i.B, i.C)
		return
	}
	if i.CarryMode == machine.CarryOperands {
		carry(19, i.D, i.E)
	} else {
		e.ri(0x03400000, "andi", 19, i.D, 1)
	}
	e.Binary(i.Arithmetic, 64, 20, i.B, i.C)
	e.Binary(i.Arithmetic, 64, i.A, 20, 19)
}
func (e *ByteAssembler) CountZeros(operation op.Opcode, w uint8, d, a backend.Register) {
	if !e.require((validGeneralRegisters(d, a)) && (validWidth(w)) && (operation == op.OpLeadingZeros || operation == op.OpTrailingZeros)) {
		return
	}

	code, name, hw := uint32(0x2400), "clz.d", uint8(64)
	if operation == op.OpTrailingZeros {
		code, name = 0x2c00, "ctz.d"
	}
	if w <= 32 {
		hw = 32
		code -= 0x1000
		name = strings.ReplaceAll(name, ".d", ".w")
	}
	// Narrow CTZ(0) must return the logical width, not the hardware width.
	if operation == op.OpTrailingZeros && w < 32 {
		if w == 8 {
			e.ri(0x03800000, "ori", 19, a, 256)
		} else {
			e.Constant(19, 1<<w)
			e.Binary(op.OpOr, 64, 19, a, 19)
		}
		a = 19
	}
	e.rr(code, name, d, a)
	if operation == op.OpLeadingZeros && w < hw {
		e.ri(0x02c00000, "addi.d", d, d, -int64(hw-w))
	}
}
func (e *ByteAssembler) AndNot(width uint8, d, a, b backend.Register) {
	if !e.require((validGeneralRegisters(d, a, b)) && (validWidth(width))) {
		return
	}
	e.rrr(0x00168000, "andn", d, a, b)
}

// Err returns the first operand encoding error; invalid assemblers yield no Bytes.
func (e *ByteAssembler) Err() error { return e.err }
