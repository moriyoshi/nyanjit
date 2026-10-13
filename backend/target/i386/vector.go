package i386

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// 32-bit mode has no REX prefixes and only XMM0-XMM7.
func vectorRR(code []byte, prefix, opcode byte, reg, rm uint8, imm ...byte) []byte {
	return append(append(code, prefix, 0x0f, opcode, 0xc0|reg<<3|rm), imm...)
}
func vectorMemory(code []byte, prefix, opcode byte, vector, base uint8, offset int) []byte {
	// A fixed disp32 keeps physical sizing independent of the outgoing call
	// area installed by FrameEnterArea after allocation.
	return appendBase32(append(code, prefix, 0x0f), opcode, backend.Register(vector), backend.Register(base), offset)
}

// vectorPlan appends the expansion of ins to p. On an error the plan may
// have appended part of it.
func vectorPlan(ins machine.Instruction, stack abi.StackPractice, callArea int, p *planCode) error {
	clearUnused(&ins)
	for _, r := range []backend.Register{ins.A, ins.B, ins.C, ins.HighA, ins.HighB, ins.HighC} {
		if backend.IsVectorRegister(r) {
			if backend.VectorNumber(r) >= 8 {
				return machine.ErrUnsupportedOperation
			}
		} else if r >= 8 {
			return machine.ErrUnsupportedOperation
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
		case op.OpVExtendUnsigned, op.OpVExtendSigned:
			valid = fp(ins.A) && fp(ins.B)
		case op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight:
			valid = fp(ins.A) && fp(ins.B)
		default:
			valid = fp(ins.A) && fp(ins.B) && fp(ins.C)
		}
	}
	if !valid {
		return machine.ErrUnsupportedOperation
	}
	rr := func(prefix, opcode byte, reg, rm uint8, imm ...byte) {
		p.code = vectorRR(p.code, prefix, opcode, reg, rm, imm...)
	}
	vr := func(r backend.Register) uint8 { return backend.VectorNumber(r) }
	vn := func(n uint8) string { return fmt.Sprintf("xmm%d", n) }
	a, b, c := vr(ins.A), vr(ins.B), vr(ins.C)
	copyVector := func(dst, src uint8) {
		if dst != src {
			rr(0x66, 0x6f, dst, src)
			if p.text {
				p.line("movdqa %s, %s", vn(dst), vn(src))
			}
		}
	}
	if ins.Op == machine.OpVectorMove {
		copyVector(a, b)
		return nil
	}
	if ins.Op == machine.OpVectorSpill || ins.Op == machine.OpVectorReload {
		base, offset := uint8(5), ins.Slot*4
		if stack == abi.StackPlatform {
			base, offset = 4, ins.Slot*4+callArea
		}
		op := byte(0x7f)
		if ins.Op == machine.OpVectorReload {
			op = 0x6f
		}
		p.code = vectorMemory(p.code, 0xf3, op, a, base, offset)
		switch {
		case !p.text:
		case ins.Op == machine.OpVectorReload:
			p.line("movdqu %s, [%s + %#x]", vn(a), name(backend.Register(base)), offset)
		default:
			p.line("movdqu [%s + %#x], %s", name(backend.Register(base)), offset, vn(a))
		}
		return nil
	}
	moveScalar := func(dst uint8, src, high backend.Register, width uint8) {
		rr(0x66, 0x6e, dst, uint8(src))
		if p.text {
			p.line("movd %s, %s", vn(dst), name(src))
		}
		if width == 64 {
			rr(0x66, 0x6e, 2, uint8(high))
			if p.text {
				p.line("movd xmm2, %s", name(high))
			}
			rr(0x66, 0x62, dst, 2)
			if p.text {
				p.line("punpckldq %s, xmm2", vn(dst))
			}
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
		p.code = vectorMemory(p.code, 0xf3, 0x6f, a, uint8(ins.B), 0)
		if p.text {
			p.line("movdqu %s, [%s]", vn(a), name(ins.B))
		}
	case op.OpVStore:
		p.code = vectorMemory(p.code, 0xf3, 0x7f, b, uint8(ins.A), 0)
		if p.text {
			p.line("movdqu [%s], %s", name(ins.A), vn(b))
		}
	case op.OpVZero:
		rr(0x66, 0xef, a, a)
		if p.text {
			p.line("pxor %s, %s", vn(a), vn(a))
		}
	case op.OpVSplat:
		moveScalar(a, ins.B, ins.HighB, ins.Bits)
		if ins.Bits == 8 {
			rr(0x66, 0x60, a, a)
			if p.text {
				p.line("punpcklbw %s, %s", vn(a), vn(a))
			}
		}
		if ins.Bits <= 16 {
			rr(0xf2, 0x70, a, a, 0)
			if p.text {
				p.line("pshuflw %s, %s, 0", vn(a), vn(a))
			}
		}
		if ins.Bits <= 32 {
			rr(0x66, 0x70, a, a, 0)
			if p.text {
				p.line("pshufd %s, %s, 0", vn(a), vn(a))
			}
		} else {
			rr(0x66, 0x6c, a, a)
			if p.text {
				p.line("punpcklqdq %s, %s", vn(a), vn(a))
			}
		}
	case op.OpVExtract:
		copyVector(0, b)
		offset := byte(ins.Imm) * ins.Bits / 8
		if offset != 0 {
			rr(0x66, 0x73, 3, 0, offset)
			if p.text {
				p.line("psrldq xmm0, %d", offset)
			}
		}
		if ins.Bits == 8 {
			rr(0x66, 0x72, 6, 0, 24)
			if p.text {
				p.line("pslld xmm0, 24")
			}
			rr(0x66, 0x72, 2, 0, 24)
			if p.text {
				p.line("psrld xmm0, 24")
			}
		}
		if ins.Bits == 16 {
			rr(0x66, 0xc5, uint8(ins.A), 0, 0)
			if p.text {
				p.line("pextrw %s, xmm0, 0", name(ins.A))
			}
		} else {
			rr(0x66, 0x7e, 0, uint8(ins.A))
			if p.text {
				p.line("movd %s, xmm0", name(ins.A))
			}
		}
		if ins.Bits == 64 {
			rr(0x66, 0x70, 2, 0, 0x55)
			if p.text {
				p.line("pshufd xmm2, xmm0, 0x55")
			}
			rr(0x66, 0x7e, 2, uint8(ins.HighA))
			if p.text {
				p.line("movd %s, xmm2", name(ins.HighA))
			}
		}
	case op.OpVInsert:
		if ins.Bits != 16 {
			return machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		rr(0x66, 0xc4, a, uint8(ins.C), byte(ins.Imm))
		if p.text {
			p.line("pinsrw %s, %s, %d", vn(a), name(ins.C), ins.Imm)
		}
	case op.OpVInsertZero:
		copyVector(a, b)
		// Public IR permits overwriting a nonzero lane. Build its byte mask
		// in the reserved XMM0, clear just that lane, then insert the scalar.
		offset := byte(ins.Imm) * ins.Bits / 8
		rr(0x66, 0x76, 0, 0)
		if p.text {
			p.line("pcmpeqd xmm0, xmm0")
		}
		rr(0x66, 0x73, 3, 0, 16-ins.Bits/8)
		if p.text {
			p.line("psrldq xmm0, %d", 16-ins.Bits/8)
		}
		if offset != 0 {
			rr(0x66, 0x73, 7, 0, offset)
			if p.text {
				p.line("pslldq xmm0, %d", offset)
			}
		}
		rr(0x66, 0xdf, 0, a)
		if p.text {
			p.line("pandn xmm0, %s", vn(a))
		}
		copyVector(a, 0)
		moveScalar(0, ins.C, ins.HighC, ins.Bits)
		if ins.Bits < 32 {
			// MOVD also copies bits above a narrow scalar; discard them before OR.
			rr(0x66, 0x73, 7, 0, 16-ins.Bits/8)
			if p.text {
				p.line("pslldq xmm0, %d", 16-ins.Bits/8)
			}
			rr(0x66, 0x73, 3, 0, 16-ins.Bits/8)
			if p.text {
				p.line("psrldq xmm0, %d", 16-ins.Bits/8)
			}
		}
		if offset != 0 {
			rr(0x66, 0x73, 7, 0, offset)
			if p.text {
				p.line("pslldq xmm0, %d", offset)
			}
		}
		rr(0x66, 0xeb, a, 0)
		if p.text {
			p.line("por %s, xmm0", vn(a))
		}
	case op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight:
		if ins.Bits != 32 {
			return machine.ErrUnsupportedOperation
		}
		count := ins.Imm
		rotate := ins.Arithmetic == op.OpRotateLeft || ins.Arithmetic == op.OpRotateRight
		if rotate {
			count %= 32
		}
		if !rotate && count >= 32 {
			rr(0x66, 0xef, a, a)
			if p.text {
				p.line("pxor %s, %s", vn(a), vn(a))
			}
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
			rr(0x66, 0x72, extension, dst, byte(count))
			if p.text {
				p.line("%s %s, %d", mnemonic, vn(dst), count)
			}
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
			rr(0x66, 0xeb, a, 0)
			if p.text {
				p.line("por %s, xmm0", vn(a))
			}
		}
	case op.OpVCarrylessMul:
		if ins.Bits != 64 || ins.Imm > 3 {
			return machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		// IR uses consecutive selector bits; Intel's immediate uses bits 0/4.
		selector := byte(ins.Imm&1 | (ins.Imm&2)<<3)
		code := []byte{0x66, 0x0f, 0x3a, 0x44, 0xc0 | a<<3 | c, selector}
		p.code = append(p.code, code...)
		if p.text {
			p.line("pclmulqdq %s, %s, %#x", vn(a), vn(c), selector)
		}
	case op.OpVShuffle, op.OpVExtendUnsigned, op.OpVExtendSigned:
		steps, ok := planShuffle(ins.Bits, ins.Imm, ins.Disp, a, b, c)
		if ins.Arithmetic != op.OpVShuffle {
			steps, ok = planExtend(ins.Arithmetic, ins.Bits, ins.Imm, a, b)
		}
		if !ok {
			return machine.ErrUnsupportedOperation
		}
		for _, step := range steps.steps[:steps.n] {
			p.code = appendSSE(p.code, step)
			switch {
			case !p.text:
			case step.imm >= 0:
				p.line("%s %s, %s, %#x", step.name, vn(step.dst), vn(step.src), step.imm)
			default:
				p.line("%s %s, %s", step.name, vn(step.dst), vn(step.src))
			}
		}
	case op.OpVCompare:
		if ins.Bits != 8 || ins.Imm != uint64(op.BranchEqual) {
			return machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		rr(0x66, 0x74, a, c)
		if p.text {
			p.line("pcmpeqb %s, %s", vn(a), vn(c))
		}
	case op.OpVMaskBits:
		rr(0x66, 0xd7, uint8(ins.A), b)
		if p.text {
			p.line("pmovmskb %s, %s", name(ins.A), vn(b))
		}
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
				return machine.ErrUnsupportedOperation
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
		rr(0x66, operation, a, c)
		if p.text {
			p.line("%s %s, %s", mnemonic, vn(a), vn(c))
		}
	default:
		return machine.ErrUnsupportedOperation
	}
	return nil
}

// Vector visits a 128-bit allocated integer SIMD operation. XMM0-XMM2
// are reserved expansion temporaries; allocated input/output homes use XMM3-XMM7.
func (e *ByteAssembler) Vector(ins machine.Instruction) error {
	p := planCode{code: e.code}
	if err := vectorPlan(ins, e.stack, e.callArea, &p); err != nil {
		return err // e.code keeps its length; a partial plan is dropped
	}
	e.code = p.code
	return nil
}

// Vector visits the identical allocated SIMD operation for text emission.
func (e *TextAssembler) Vector(ins machine.Instruction) error {
	p := planCode{text: true, code: e.code}
	if err := vectorPlan(ins, e.stack, e.callArea, &p); err != nil {
		return err
	}
	e.code = p.code
	e.recordPlan(&p)
	return nil
}

// VectorSize measures the expansion before physical PC assignment.
func VectorSize(ins machine.Instruction, stack abi.StackPractice, callArea int) int {
	var buffer [64]byte
	p := planCode{code: buffer[:0]}
	if vectorPlan(ins, stack, callArea, &p) != nil {
		return 0
	}
	return len(p.code)
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
