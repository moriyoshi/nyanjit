package i386

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

type vectorConcrete struct {
	code []byte
	text string
}

// 32-bit mode has no REX prefixes and only XMM0-XMM7.
func vectorRR(prefix, opcode byte, reg, rm uint8, imm ...byte) []byte {
	return append([]byte{prefix, 0x0f, opcode, 0xc0 | reg<<3 | rm}, imm...)
}
func vectorMemory(prefix, opcode byte, vector, base uint8, offset int) []byte {
	// A fixed disp32 keeps physical sizing independent of the outgoing call
	// area installed by FrameEnterArea after allocation.
	return appendBase32([]byte{prefix, 0x0f}, opcode, backend.Register(vector), backend.Register(base), offset)
}
func vectorPlan(ins machine.Instruction, stack abi.StackPractice, callArea int, text bool) ([]vectorConcrete, error) {
	format := func(f string, args ...any) string {
		if !text {
			return ""
		}
		return fmt.Sprintf(f, args...)
	}
	for _, r := range []backend.Register{ins.A, ins.B, ins.C, ins.HighA, ins.HighB, ins.HighC} {
		if backend.IsVectorRegister(r) {
			if backend.VectorNumber(r) >= 8 {
				return nil, machine.ErrUnsupportedOperation
			}
		} else if r >= 8 {
			return nil, machine.ErrUnsupportedOperation
		}
	}
	gp := func(r backend.Register) bool { return r < 8 }
	fp := func(r backend.Register) bool { return backend.IsVectorRegister(r) && backend.VectorNumber(r) < 8 }
	valid := fp(ins.A)
	switch ins.Op {
	case machine.OpVectorMove:
		valid = fp(ins.A) && fp(ins.B)
	case machine.OpVectorSpill, machine.OpVectorReload:
		valid = fp(ins.A)
	default:
		switch ins.Arithmetic {
		case op.OpVLoad:
			valid = fp(ins.A) && gp(ins.B)
		case op.OpVStore:
			valid = gp(ins.A) && fp(ins.B)
		case op.OpVSplat:
			valid = fp(ins.A) && gp(ins.B) && (ins.Bits != 64 || gp(ins.HighB))
		case op.OpVExtract:
			valid = gp(ins.A) && fp(ins.B) && (ins.Bits != 64 || gp(ins.HighA) && ins.HighA != ins.A)
		case op.OpVInsert, op.OpVInsertZero:
			valid = fp(ins.A) && fp(ins.B) && gp(ins.C) && (ins.Bits != 64 || gp(ins.HighC))
		case op.OpVMaskBits:
			valid = gp(ins.A) && fp(ins.B)
		case op.OpVZero:
			valid = fp(ins.A)
		case op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight:
			valid = fp(ins.A) && fp(ins.B)
		default:
			valid = fp(ins.A) && fp(ins.B) && fp(ins.C)
		}
	}
	if !valid {
		return nil, machine.ErrUnsupportedOperation
	}
	plan := []vectorConcrete{}
	emit := func(code []byte, text string) { plan = append(plan, vectorConcrete{code, text}) }
	vr := func(r backend.Register) uint8 { return backend.VectorNumber(r) }
	vn := func(n uint8) string { return format("xmm%d", n) }
	a, b, c := vr(ins.A), vr(ins.B), vr(ins.C)
	copyVector := func(dst, src uint8) {
		if dst != src {
			emit(vectorRR(0x66, 0x6f, dst, src), format("movdqa %s, %s", vn(dst), vn(src)))
		}
	}
	if ins.Op == machine.OpVectorMove {
		copyVector(a, b)
		return plan, nil
	}
	if ins.Op == machine.OpVectorSpill || ins.Op == machine.OpVectorReload {
		base, offset := uint8(5), ins.Slot*4
		if stack == abi.StackPlatform {
			base, offset = 4, ins.Slot*4+callArea
		}
		op, code := byte(0x7f), "movdqu"
		text := format("%s [%s + %#x], %s", code, name(backend.Register(base)), offset, vn(a))
		if ins.Op == machine.OpVectorReload {
			op = 0x6f
			text = format("%s %s, [%s + %#x]", code, vn(a), name(backend.Register(base)), offset)
		}
		emit(vectorMemory(0xf3, op, a, base, offset), text)
		return plan, nil
	}
	moveScalar := func(dst uint8, src, high backend.Register, width uint8) {
		emit(vectorRR(0x66, 0x6e, dst, uint8(src)), format("movd %s, %s", vn(dst), name(src)))
		if width == 64 {
			emit(vectorRR(0x66, 0x6e, 2, uint8(high)), format("movd xmm2, %s", name(high)))
			emit(vectorRR(0x66, 0x62, dst, 2), format("punpckldq %s, xmm2", vn(dst)))
		}
	}
	// Two-address arithmetic must preserve the old right operand before copying
	// left into an aliased destination. Homes exclude the XMM0-XMM2 temps.
	switch ins.Arithmetic {
	case op.OpAdd, op.OpSub, op.OpMul, op.OpAnd, op.OpOr, op.OpXor, op.OpVCompare, op.OpVCarrylessMul:
		if a == c && a != b {
			copyVector(1, c)
			c = 1
		}
	}
	switch ins.Arithmetic {
	case op.OpVLoad:
		emit(vectorMemory(0xf3, 0x6f, a, uint8(ins.B), 0), format("movdqu %s, [%s]", vn(a), name(ins.B)))
	case op.OpVStore:
		emit(vectorMemory(0xf3, 0x7f, b, uint8(ins.A), 0), format("movdqu [%s], %s", name(ins.A), vn(b)))
	case op.OpVZero:
		emit(vectorRR(0x66, 0xef, a, a), format("pxor %s, %s", vn(a), vn(a)))
	case op.OpVSplat:
		moveScalar(a, ins.B, ins.HighB, ins.Bits)
		if ins.Bits == 8 {
			emit(vectorRR(0x66, 0x60, a, a), format("punpcklbw %s, %s", vn(a), vn(a)))
		}
		if ins.Bits <= 16 {
			emit(vectorRR(0xf2, 0x70, a, a, 0), format("pshuflw %s, %s, 0", vn(a), vn(a)))
		}
		if ins.Bits <= 32 {
			emit(vectorRR(0x66, 0x70, a, a, 0), format("pshufd %s, %s, 0", vn(a), vn(a)))
		} else {
			emit(vectorRR(0x66, 0x6c, a, a), format("punpcklqdq %s, %s", vn(a), vn(a)))
		}
	case op.OpVExtract:
		copyVector(0, b)
		offset := byte(ins.Imm) * ins.Bits / 8
		if offset != 0 {
			emit(vectorRR(0x66, 0x73, 3, 0, offset), format("psrldq xmm0, %d", offset))
		}
		if ins.Bits == 8 {
			emit(vectorRR(0x66, 0x72, 6, 0, 24), "pslld xmm0, 24")
			emit(vectorRR(0x66, 0x72, 2, 0, 24), "psrld xmm0, 24")
		}
		if ins.Bits == 16 {
			emit(vectorRR(0x66, 0xc5, uint8(ins.A), 0, 0), format("pextrw %s, xmm0, 0", name(ins.A)))
		} else {
			emit(vectorRR(0x66, 0x7e, 0, uint8(ins.A)), format("movd %s, xmm0", name(ins.A)))
		}
		if ins.Bits == 64 {
			emit(vectorRR(0x66, 0x70, 2, 0, 0x55), "pshufd xmm2, xmm0, 0x55")
			emit(vectorRR(0x66, 0x7e, 2, uint8(ins.HighA)), format("movd %s, xmm2", name(ins.HighA)))
		}
	case op.OpVInsert:
		if ins.Bits != 16 {
			return nil, machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		emit(vectorRR(0x66, 0xc4, a, uint8(ins.C), byte(ins.Imm)), format("pinsrw %s, %s, %d", vn(a), name(ins.C), ins.Imm))
	case op.OpVInsertZero:
		copyVector(a, b)
		// Public IR permits overwriting a nonzero lane. Build its byte mask
		// in the reserved XMM0, clear just that lane, then insert the scalar.
		offset := byte(ins.Imm) * ins.Bits / 8
		emit(vectorRR(0x66, 0x76, 0, 0), "pcmpeqd xmm0, xmm0")
		emit(vectorRR(0x66, 0x73, 3, 0, 16-ins.Bits/8), format("psrldq xmm0, %d", 16-ins.Bits/8))
		if offset != 0 {
			emit(vectorRR(0x66, 0x73, 7, 0, offset), format("pslldq xmm0, %d", offset))
		}
		emit(vectorRR(0x66, 0xdf, 0, a), format("pandn xmm0, %s", vn(a)))
		copyVector(a, 0)
		moveScalar(0, ins.C, ins.HighC, ins.Bits)
		if ins.Bits < 32 {
			// MOVD also copies bits above a narrow scalar; discard them before OR.
			emit(vectorRR(0x66, 0x73, 7, 0, 16-ins.Bits/8), format("pslldq xmm0, %d", 16-ins.Bits/8))
			emit(vectorRR(0x66, 0x73, 3, 0, 16-ins.Bits/8), format("psrldq xmm0, %d", 16-ins.Bits/8))
		}
		if offset != 0 {
			emit(vectorRR(0x66, 0x73, 7, 0, offset), format("pslldq xmm0, %d", offset))
		}
		emit(vectorRR(0x66, 0xeb, a, 0), format("por %s, xmm0", vn(a)))
	case op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight:
		if ins.Bits != 32 {
			return nil, machine.ErrUnsupportedOperation
		}
		count := ins.Imm
		rotate := ins.Arithmetic == op.OpRotateLeft || ins.Arithmetic == op.OpRotateRight
		if rotate {
			count %= 32
		}
		if !rotate && count >= 32 {
			emit(vectorRR(0x66, 0xef, a, a), format("pxor %s, %s", vn(a), vn(a)))
			break
		}
		if count == 0 {
			copyVector(a, b)
			break
		}
		left := ins.Arithmetic == op.OpShiftLeft || ins.Arithmetic == op.OpRotateLeft
		shift := func(dst uint8, left bool, count uint64) {
			extension, mnemonic := uint8(2), "psrld"
			if left {
				extension, mnemonic = 6, "pslld"
			}
			emit(vectorRR(0x66, 0x72, extension, dst, byte(count)), format("%s %s, %d", mnemonic, vn(dst), count))
		}
		// XMM0 is an expansion temporary; allocator homes never use it.
		// Preserve the original input before an in-place destination shift.
		if rotate {
			copyVector(0, b)
		}
		copyVector(a, b)
		shift(a, left, count)
		if rotate {
			shift(0, !left, 32-count)
			emit(vectorRR(0x66, 0xeb, a, 0), format("por %s, xmm0", vn(a)))
		}
	case op.OpVCarrylessMul:
		if ins.Bits != 64 || ins.Imm > 3 {
			return nil, machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		// IR uses consecutive selector bits; Intel's immediate uses bits 0/4.
		selector := byte(ins.Imm&1 | (ins.Imm&2)<<3)
		code := []byte{0x66, 0x0f, 0x3a, 0x44, 0xc0 | a<<3 | c, selector}
		emit(code, format("pclmulqdq %s, %s, %#x", vn(a), vn(c), selector))
	case op.OpVCompare:
		if ins.Bits != 8 || ins.Imm != uint64(op.BranchEqual) {
			return nil, machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		emit(vectorRR(0x66, 0x74, a, c), format("pcmpeqb %s, %s", vn(a), vn(c)))
	case op.OpVMaskBits:
		emit(vectorRR(0x66, 0xd7, uint8(ins.A), b), format("pmovmskb %s, %s", name(ins.A), vn(b)))
	case op.OpAdd, op.OpSub, op.OpMul, op.OpAnd, op.OpOr, op.OpXor:
		operation := byte(0)
		mnemonic := ""
		size := laneSize(ins.Bits)
		switch ins.Arithmetic {
		case op.OpAdd:
			operation = []byte{0xfc, 0xfd, 0xfe, 0xd4}[size]
			mnemonic = []string{"paddb", "paddw", "paddd", "paddq"}[size]
		case op.OpSub:
			operation = []byte{0xf8, 0xf9, 0xfa, 0xfb}[size]
			mnemonic = []string{"psubb", "psubw", "psubd", "psubq"}[size]
		case op.OpMul:
			if ins.Bits != 16 {
				return nil, machine.ErrUnsupportedOperation
			}
			operation, mnemonic = 0xd5, "pmullw"
		case op.OpAnd:
			operation, mnemonic = 0xdb, "pand"
		case op.OpOr:
			operation, mnemonic = 0xeb, "por"
		case op.OpXor:
			operation, mnemonic = 0xef, "pxor"
		}
		copyVector(a, b)
		emit(vectorRR(0x66, operation, a, c), format("%s %s, %s", mnemonic, vn(a), vn(c)))
	default:
		return nil, machine.ErrUnsupportedOperation
	}
	return plan, nil
}

// Vector visits a 128-bit allocated integer SIMD operation. XMM0-XMM2
// are reserved expansion temporaries; allocated input/output homes use XMM3-XMM7.
func (e *ByteAssembler) Vector(ins machine.Instruction) error {
	plan, err := vectorPlan(ins, e.stack, e.callArea, false)
	if err != nil {
		return err
	}
	for _, native := range plan {
		e.code = append(e.code, native.code...)
	}
	return nil
}

// Vector visits the identical allocated SIMD operation for text emission.
func (e *TextAssembler) Vector(ins machine.Instruction) error {
	plan, err := vectorPlan(ins, e.stack, e.callArea, true)
	if err != nil {
		return err
	}
	for _, native := range plan {
		e.code = append(e.code, native.code...)
		e.record("%s", native.text)
	}
	return nil
}

// VectorSize measures the expansion before physical PC assignment.
func VectorSize(ins machine.Instruction, stack abi.StackPractice, callArea int) int {
	plan, err := vectorPlan(ins, stack, callArea, false)
	if err != nil {
		return 0
	}
	size := 0
	for _, n := range plan {
		size += len(n.code)
	}
	return size
}

// laneSize indexes 8-, 16-, 32- and 64-bit lane forms as 0 to 3.
func laneSize(bits uint8) int {
	switch bits {
	case 16:
		return 1
	case 32:
		return 2
	case 64:
		return 3
	}
	return 0
}
