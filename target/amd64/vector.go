package amd64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

type vectorConcrete struct {
	code []byte
	text string
}

func vectorRR(prefix, opcode byte, reg, rm uint8, wide bool, imm ...byte) []byte {
	out := []byte{prefix}
	rex := byte(0x40 | (reg>>3)<<2 | rm>>3)
	if wide {
		rex |= 8
	}
	if rex != 0x40 {
		out = append(out, rex)
	}
	out = append(out, 0x0f, opcode, 0xc0|(reg&7)<<3|rm&7)
	return append(out, imm...)
}
func vectorMemory(prefix, opcode byte, vector, base uint8, offset int) []byte {
	out := []byte{prefix}
	rex := byte(0x40 | (vector>>3)<<2 | base>>3)
	if rex != 0x40 {
		out = append(out, rex)
	}
	mod := byte(0)
	if offset != 0 || base&7 == 5 {
		mod = 0x40
		if offset < -128 || offset > 127 {
			mod = 0x80
		}
	}
	out = append(out, 0x0f, opcode, mod|(vector&7)<<3|base&7)
	if base&7 == 4 {
		out = append(out, 0x24)
	}
	if mod == 0x40 {
		out = append(out, byte(offset))
	} else if mod == 0x80 {
		out = append(out, byte(offset), byte(offset>>8), byte(offset>>16), byte(offset>>24))
	}
	return out
}
func vectorPlan(ins machine.Instruction, stack abi.StackPractice, homes int) ([]vectorConcrete, error) {
	plan := []vectorConcrete{}
	emit := func(code []byte, text string) { plan = append(plan, vectorConcrete{code, text}) }
	vr := func(r machine.Register) uint8 { return machine.VectorNumber(r) }
	vn := func(n uint8) string { return fmt.Sprintf("xmm%d", n) }
	a, b, c := vr(ins.A), vr(ins.B), vr(ins.C)
	copyVector := func(dst, src uint8) {
		if dst != src {
			emit(vectorRR(0x66, 0x6f, dst, src, false), fmt.Sprintf("movdqa %s, %s", vn(dst), vn(src)))
		}
	}
	if ins.Op == machine.OpVectorMove {
		copyVector(a, b)
		return plan, nil
	}
	if ins.Op == machine.OpVectorSpill || ins.Op == machine.OpVectorReload {
		base, offset := uint8(10), ins.Slot*8
		if stack == abi.StackPlatform {
			base, offset = 4, (ins.Slot+homes)*8
		}
		op, code := byte(0x7f), "movdqu"
		text := fmt.Sprintf("%s [%s + %#x], %s", code, amd64RegisterNames[base], offset, vn(a))
		if ins.Op == machine.OpVectorReload {
			op = 0x6f
			text = fmt.Sprintf("%s %s, [%s + %#x]", code, vn(a), amd64RegisterNames[base], offset)
		}
		emit(vectorMemory(0xf3, op, a, base, offset), text)
		return plan, nil
	}
	moveScalar := func(dst uint8, src machine.Register, width uint8) {
		mnemonic := "movd"
		names := amd64RegisterNames32
		if width == 64 {
			mnemonic = "movq"
			names = amd64RegisterNames
		}
		emit(vectorRR(0x66, 0x6e, dst, uint8(src), width == 64), fmt.Sprintf("%s %s, %s", mnemonic, vn(dst), names[src]))
	}
	switch ins.Arithmetic {
	case ir.OpVLoad:
		emit(vectorMemory(0xf3, 0x6f, a, uint8(ins.B), 0), fmt.Sprintf("movdqu %s, [%s]", vn(a), amd64RegisterNames[ins.B]))
	case ir.OpVStore:
		emit(vectorMemory(0xf3, 0x7f, b, uint8(ins.A), 0), fmt.Sprintf("movdqu [%s], %s", amd64RegisterNames[ins.A], vn(b)))
	case ir.OpVZero:
		emit(vectorRR(0x66, 0xef, a, a, false), fmt.Sprintf("pxor %s, %s", vn(a), vn(a)))
	case ir.OpVSplat:
		moveScalar(a, ins.B, ins.Bits)
		if ins.Bits == 8 {
			emit(vectorRR(0x66, 0x60, a, a, false), fmt.Sprintf("punpcklbw %s, %s", vn(a), vn(a)))
		}
		if ins.Bits <= 16 {
			emit(vectorRR(0xf2, 0x70, a, a, false, 0), fmt.Sprintf("pshuflw %s, %s, 0", vn(a), vn(a)))
		}
		if ins.Bits <= 32 {
			emit(vectorRR(0x66, 0x70, a, a, false, 0), fmt.Sprintf("pshufd %s, %s, 0", vn(a), vn(a)))
		} else {
			emit(vectorRR(0x66, 0x6c, a, a, false), fmt.Sprintf("punpcklqdq %s, %s", vn(a), vn(a)))
		}
	case ir.OpVExtract:
		copyVector(0, b)
		offset := byte(ins.Imm) * ins.Bits / 8
		if offset != 0 {
			emit(vectorRR(0x66, 0x73, 3, 0, false, offset), fmt.Sprintf("psrldq xmm0, %d", offset))
		}
		emit(vectorRR(0x66, 0x7e, 0, uint8(ins.A), true), fmt.Sprintf("movq %s, xmm0", amd64RegisterNames[ins.A]))
		if ins.Bits < 64 {
			bytes := NewByteAssembler(stack, homes)
			bytes.Narrow(ins.A, ins.A, ins.Bits)
			normalization := fmt.Sprintf("movzx %s, %s", amd64RegisterNames32[ins.A], amd64RegisterNames8[ins.A])
			if ins.Bits == 16 {
				normalization = fmt.Sprintf("movzx %s, %s", amd64RegisterNames32[ins.A], amd64RegisterNames16[ins.A])
			}
			if ins.Bits == 32 {
				normalization = fmt.Sprintf("mov %s, %s", amd64RegisterNames32[ins.A], amd64RegisterNames32[ins.A])
			}
			emit(bytes.Bytes(), normalization)
		}
	case ir.OpVInsert:
		if ins.Bits != 16 {
			return nil, machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		emit(vectorRR(0x66, 0xc4, a, uint8(ins.C), false, byte(ins.Imm)), fmt.Sprintf("pinsrw %s, %s, %d", vn(a), amd64RegisterNames32[ins.C], ins.Imm))
	case ir.OpVInsertZero:
		copyVector(a, b)
		moveScalar(0, ins.C, ins.Bits)
		offset := byte(ins.Imm) * ins.Bits / 8
		if offset != 0 {
			emit(vectorRR(0x66, 0x73, 7, 0, false, offset), fmt.Sprintf("pslldq xmm0, %d", offset))
		}
		emit(vectorRR(0x66, 0xeb, a, 0, false), fmt.Sprintf("por %s, xmm0", vn(a)))
	case ir.OpVCompare:
		if ins.Bits != 8 || ins.Imm != uint64(ir.BranchEqual) {
			return nil, machine.ErrUnsupportedOperation
		}
		copyVector(a, b)
		emit(vectorRR(0x66, 0x74, a, c, false), fmt.Sprintf("pcmpeqb %s, %s", vn(a), vn(c)))
	case ir.OpVMaskBits:
		emit(vectorRR(0x66, 0xd7, uint8(ins.A), b, false), fmt.Sprintf("pmovmskb %s, %s", amd64RegisterNames32[ins.A], vn(b)))
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
		op := byte(0)
		mnemonic := ""
		size := map[uint8]int{8: 0, 16: 1, 32: 2, 64: 3}[ins.Bits]
		switch ins.Arithmetic {
		case ir.OpAdd:
			op = []byte{0xfc, 0xfd, 0xfe, 0xd4}[size]
			mnemonic = []string{"paddb", "paddw", "paddd", "paddq"}[size]
		case ir.OpSub:
			op = []byte{0xf8, 0xf9, 0xfa, 0xfb}[size]
			mnemonic = []string{"psubb", "psubw", "psubd", "psubq"}[size]
		case ir.OpMul:
			if ins.Bits != 16 {
				return nil, machine.ErrUnsupportedOperation
			}
			op, mnemonic = 0xd5, "pmullw"
		case ir.OpAnd:
			op, mnemonic = 0xdb, "pand"
		case ir.OpOr:
			op, mnemonic = 0xeb, "por"
		case ir.OpXor:
			op, mnemonic = 0xef, "pxor"
		}
		copyVector(a, b)
		emit(vectorRR(0x66, op, a, c, false), fmt.Sprintf("%s %s, %s", mnemonic, vn(a), vn(c)))
	default:
		return nil, machine.ErrUnsupportedOperation
	}
	return plan, nil
}

// Vector visits a 128-bit allocated integer SIMD operation.
func (e *ByteAssembler) Vector(ins machine.Instruction) error {
	plan, err := vectorPlan(ins, e.stack, e.goHomes)
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
	plan, err := vectorPlan(ins, e.stack, e.goHomes)
	if err != nil {
		return err
	}
	for _, native := range plan {
		e.line(len(native.code), "%s", native.text)
	}
	return nil
}

// VectorSize measures the expansion before physical PC assignment.
func VectorSize(ins machine.Instruction, stack abi.StackPractice, homes int) int {
	plan, err := vectorPlan(ins, stack, homes)
	if err != nil {
		return 0
	}
	size := 0
	for _, n := range plan {
		size += len(n.code)
	}
	return size
}
