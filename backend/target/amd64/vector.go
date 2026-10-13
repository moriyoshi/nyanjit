package amd64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// vectorLine is one native instruction of a vector or float expansion in
// text mode. A plan appends the bytes of each instruction to code and, given
// lines, formats a line for it. Sizing passes a stack buffer and no lines,
// and plans test text before formatting, so sizing does not allocate.
type vectorLine struct {
	size int
	text string
}

func vectorRR(code []byte, prefix, opcode byte, reg, rm uint8, wide bool, imm ...byte) []byte {
	code = append(code, prefix)
	rex := byte(0x40 | (reg>>3)<<2 | rm>>3)
	if wide {
		rex |= 8
	}
	if rex != 0x40 {
		code = append(code, rex)
	}
	code = append(code, 0x0f, opcode, 0xc0|(reg&7)<<3|rm&7)
	return append(code, imm...)
}
func vectorMemory(code []byte, prefix, opcode byte, vector, base uint8, offset int) []byte {
	code = append(code, prefix)
	rex := byte(0x40 | (vector>>3)<<2 | base>>3)
	if rex != 0x40 {
		code = append(code, rex)
	}
	mod := byte(0)
	if offset != 0 || base&7 == 5 {
		mod = 0x40
		if offset < -128 || offset > 127 {
			mod = 0x80
		}
	}
	code = append(code, 0x0f, opcode, mod|(vector&7)<<3|base&7)
	if base&7 == 4 {
		code = append(code, 0x24)
	}
	if mod == 0x40 {
		code = append(code, byte(offset))
	} else if mod == 0x80 {
		code = append(code, byte(offset), byte(offset>>8), byte(offset>>16), byte(offset>>24))
	}
	return code
}
func vectorPlan(code []byte, ins machine.Instruction, stack abi.StackPractice, homes int, lines *[]vectorLine) ([]byte, error) {
	if !validVectorOperands(ins) || !validSlot(ins.Slot, homes) {
		return code, machine.ErrUnsupportedOperation
	}
	text := lines != nil
	mark := len(code)
	line := func(format string, args ...any) {
		*lines = append(*lines, vectorLine{len(code) - mark, fmt.Sprintf(format, args...)})
		mark = len(code)
	}
	rr := func(prefix, opcode byte, reg, rm uint8, wide bool, imm ...byte) {
		code = vectorRR(code, prefix, opcode, reg, rm, wide, imm...)
	}
	vr := func(r backend.Register) uint8 { return backend.VectorNumber(r) }
	vn := func(n uint8) string { return fmt.Sprintf("xmm%d", n) }
	a, b, c := vr(ins.A), vr(ins.B), vr(ins.C)
	copyVector := func(dst, src uint8) {
		if dst != src {
			rr(0x66, 0x6f, dst, src, false)
			if text {
				line("movdqa %s, %s", vn(dst), vn(src))
			}
		}
	}
	if ins.Op == machine.OpVectorMove {
		copyVector(a, b)
		return code, nil
	}
	if ins.Op == machine.OpVectorSpill || ins.Op == machine.OpVectorReload {
		base, offset := uint8(10), ins.Slot*8
		if stack == abi.StackPlatform {
			base, offset = 4, (ins.Slot+homes)*8
		}
		op, mnemonic := byte(0x7f), "movdqu"
		if ins.Op == machine.OpVectorReload {
			op = 0x6f
		}
		code = vectorMemory(code, 0xf3, op, a, base, offset)
		if text && ins.Op == machine.OpVectorReload {
			line("%s %s, [%s + %#x]", mnemonic, vn(a), amd64RegisterNames[base], offset)
		} else if text {
			line("%s [%s + %#x], %s", mnemonic, amd64RegisterNames[base], offset, vn(a))
		}
		return code, nil
	}
	moveScalar := func(dst uint8, src backend.Register, width uint8) {
		mnemonic := "movd"
		names := amd64RegisterNames32
		if width == 64 {
			mnemonic = "movq"
			names = amd64RegisterNames
		}
		rr(0x66, 0x6e, dst, uint8(src), width == 64)
		if text {
			line("%s %s, %s", mnemonic, vn(dst), names[src])
		}
	}
	switch ins.Arithmetic {
	case op.OpVLoad:
		code = vectorMemory(code, 0xf3, 0x6f, a, uint8(ins.B), 0)
		if text {
			line("movdqu %s, [%s]", vn(a), amd64RegisterNames[ins.B])
		}
	case op.OpVStore:
		code = vectorMemory(code, 0xf3, 0x7f, b, uint8(ins.A), 0)
		if text {
			line("movdqu [%s], %s", amd64RegisterNames[ins.A], vn(b))
		}
	case op.OpVZero:
		rr(0x66, 0xef, a, a, false)
		if text {
			line("pxor %s, %s", vn(a), vn(a))
		}
	case op.OpVSplat:
		moveScalar(a, ins.B, ins.Bits)
		if ins.Bits == 8 {
			rr(0x66, 0x60, a, a, false)
			if text {
				line("punpcklbw %s, %s", vn(a), vn(a))
			}
		}
		if ins.Bits <= 16 {
			rr(0xf2, 0x70, a, a, false, 0)
			if text {
				line("pshuflw %s, %s, 0", vn(a), vn(a))
			}
		}
		if ins.Bits <= 32 {
			rr(0x66, 0x70, a, a, false, 0)
			if text {
				line("pshufd %s, %s, 0", vn(a), vn(a))
			}
		} else {
			rr(0x66, 0x6c, a, a, false)
			if text {
				line("punpcklqdq %s, %s", vn(a), vn(a))
			}
		}
	case op.OpVExtract:
		copyVector(0, b)
		offset := byte(ins.Imm) * ins.Bits / 8
		if offset != 0 {
			rr(0x66, 0x73, 3, 0, false, offset)
			if text {
				line("psrldq xmm0, %d", offset)
			}
		}
		rr(0x66, 0x7e, 0, uint8(ins.A), true)
		if text {
			line("movq %s, xmm0", amd64RegisterNames[ins.A])
		}
		if ins.Bits < 64 {
			code = appendNarrow(code, ins.A, ins.A, ins.Bits)
			switch {
			case !text:
			case ins.Bits == 16:
				line("movzx %s, %s", amd64RegisterNames32[ins.A], amd64RegisterNames16[ins.A])
			case ins.Bits == 32:
				line("mov %s, %s", amd64RegisterNames32[ins.A], amd64RegisterNames32[ins.A])
			default:
				line("movzx %s, %s", amd64RegisterNames32[ins.A], amd64RegisterNames8[ins.A])
			}
		}
	case op.OpVInsert:
		if ins.Bits != 16 {
			return code, machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		rr(0x66, 0xc4, a, uint8(ins.C), false, byte(ins.Imm))
		if text {
			line("pinsrw %s, %s, %d", vn(a), amd64RegisterNames32[ins.C], ins.Imm)
		}
	case op.OpVInsertZero:
		copyVector(a, b)
		// Public IR permits overwriting a nonzero lane. Build its byte mask
		// in the reserved XMM0, clear just that lane, then insert the scalar.
		offset := byte(ins.Imm) * ins.Bits / 8
		rr(0x66, 0x76, 0, 0, false)
		if text {
			line("pcmpeqd xmm0, xmm0")
		}
		rr(0x66, 0x73, 3, 0, false, 16-ins.Bits/8)
		if text {
			line("psrldq xmm0, %d", 16-ins.Bits/8)
		}
		if offset != 0 {
			rr(0x66, 0x73, 7, 0, false, offset)
			if text {
				line("pslldq xmm0, %d", offset)
			}
		}
		rr(0x66, 0xdf, 0, a, false)
		if text {
			line("pandn xmm0, %s", vn(a))
		}
		copyVector(a, 0)
		moveScalar(0, ins.C, ins.Bits)
		if ins.Bits < 32 {
			// MOVD also copies bits above a narrow scalar; discard them before OR.
			rr(0x66, 0x73, 7, 0, false, 16-ins.Bits/8)
			if text {
				line("pslldq xmm0, %d", 16-ins.Bits/8)
			}
			rr(0x66, 0x73, 3, 0, false, 16-ins.Bits/8)
			if text {
				line("psrldq xmm0, %d", 16-ins.Bits/8)
			}
		}
		if offset != 0 {
			rr(0x66, 0x73, 7, 0, false, offset)
			if text {
				line("pslldq xmm0, %d", offset)
			}
		}
		rr(0x66, 0xeb, a, 0, false)
		if text {
			line("por %s, xmm0", vn(a))
		}
	case op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight:
		if ins.Bits != 32 {
			return code, machine.ErrUnsupportedOperation
		}
		count := ins.Imm
		rotate := ins.Arithmetic == op.OpRotateLeft || ins.Arithmetic == op.OpRotateRight
		if rotate {
			count %= 32
		}
		if !rotate && count >= 32 {
			rr(0x66, 0xef, a, a, false)
			if text {
				line("pxor %s, %s", vn(a), vn(a))
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
			rr(0x66, 0x72, extension, dst, false, byte(count))
			if text {
				line("%s %s, %d", mnemonic, vn(dst), count)
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
			rr(0x66, 0xeb, a, 0, false)
			if text {
				line("por %s, xmm0", vn(a))
			}
		}
	case op.OpVCarrylessMul:
		if ins.Bits != 64 || ins.Imm > 3 {
			return code, machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		// IR uses consecutive selector bits; Intel's immediate uses bits 0/4.
		selector := byte(ins.Imm&1 | (ins.Imm&2)<<3)
		code = append(code, 0x66)
		if rex := byte(0x40 | (a>>3)<<2 | c>>3); rex != 0x40 {
			code = append(code, rex)
		}
		code = append(code, 0x0f, 0x3a, 0x44, 0xc0|(a&7)<<3|c&7, selector)
		if text {
			line("pclmulqdq %s, %s, %#x", vn(a), vn(c), selector)
		}
	case op.OpVCompare:
		if ins.Bits != 8 || ins.Imm != uint64(op.BranchEqual) {
			return code, machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		rr(0x66, 0x74, a, c, false)
		if text {
			line("pcmpeqb %s, %s", vn(a), vn(c))
		}
	case op.OpVMaskBits:
		rr(0x66, 0xd7, uint8(ins.A), b, false)
		if text {
			line("pmovmskb %s, %s", amd64RegisterNames32[ins.A], vn(b))
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
				return code, machine.ErrUnsupportedOperation
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
		rr(0x66, operation, a, c, false)
		if text {
			line("%s %s, %s", mnemonic, vn(a), vn(c))
		}
	default:
		return code, machine.ErrUnsupportedOperation
	}
	return code, nil
}

// Vector visits a 128-bit allocated integer SIMD operation.
func (e *ByteAssembler) Vector(ins machine.Instruction) error {
	if !e.require((validInstructionRegisters(ins))) {
		return machine.ErrUnsupportedOperation
	}

	code, err := vectorPlan(e.code, ins, e.stack, e.goHomes, nil)
	if err != nil {
		return err
	}
	e.code = code
	return nil
}

// Vector visits the identical allocated SIMD operation for text emission.
func (e *TextAssembler) Vector(ins machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.Vector(ins) }); err != nil {
		return err
	}
	var lines []vectorLine
	if _, err := vectorPlan(nil, ins, e.stack, e.goHomes, &lines); err != nil {
		return err
	}
	for _, native := range lines {
		e.line(native.size, "%s", native.text)
	}
	return nil
}

// VectorSize measures the expansion before physical PC assignment.
func VectorSize(ins machine.Instruction, stack abi.StackPractice, homes int) int {
	var buf [64]byte
	code, err := vectorPlan(buf[:0], ins, stack, homes, nil)
	if err != nil {
		return 0
	}
	return len(code)
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
