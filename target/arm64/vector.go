package arm64

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

type vectorConcrete struct {
	word uint32
	text string
}

func vectorPlan(ins machine.Instruction, stack abi.StackPractice, homes int) ([]vectorConcrete, error) {
	plan := []vectorConcrete{}
	emit := func(word uint32, text string) { plan = append(plan, vectorConcrete{word, text}) }
	vr := func(r machine.Register) uint32 { return uint32(machine.VectorNumber(r)) }
	a, b, c := vr(ins.A), vr(ins.B), vr(ins.C)
	copyVector := func(dst, src uint32) {
		if dst != src {
			emit(0x4ea01c00|src<<16|src<<5|dst, fmt.Sprintf("mov V%d.16B, V%d.16B", dst, src))
		}
	}
	if ins.Op == machine.OpVectorMove {
		copyVector(a, b)
		return plan, nil
	}
	if ins.Op == machine.OpVectorSpill || ins.Op == machine.OpVectorReload {
		base, offset := uint32(8), ins.Slot*8
		if stack == abi.StackPlatform {
			base, offset = 31, (ins.Slot+1+homes)*8
		}
		if offset < 0 || offset%16 != 0 || offset/16 > 4095 {
			return nil, machine.ErrUnsupportedOperation
		}
		word, mnemonic := uint32(0x3d800000), "str"
		if ins.Op == machine.OpVectorReload {
			word, mnemonic = 0x3dc00000, "ldr"
		}
		baseName := fmt.Sprintf("X%d", base)
		if base == 31 {
			baseName = "SP"
		}
		emit(word|uint32(offset/16)<<10|base<<5|a, fmt.Sprintf("%s Q%d, [%s, #%d]", mnemonic, a, baseName, offset))
		return plan, nil
	}
	size := map[uint8]uint32{8: 0, 16: 1, 32: 2, 64: 3}[ins.Bits]
	suffix := map[uint8]string{8: "16B", 16: "8H", 32: "4S", 64: "2D"}[ins.Bits]
	switch ins.Arithmetic {
	case ir.OpVLoad:
		emit(0x3dc00000|uint32(ins.B)<<5|a, fmt.Sprintf("ldr Q%d, [X%d]", a, ins.B))
	case ir.OpVStore:
		emit(0x3d800000|uint32(ins.A)<<5|b, fmt.Sprintf("str Q%d, [X%d]", b, ins.A))
	case ir.OpVZero:
		emit(0x4f00e400|a, fmt.Sprintf("movi V%d.16B, #0", a))
	case ir.OpVSplat:
		gpr := "W"
		if ins.Bits == 64 {
			gpr = "X"
		}
		emit(0x4e000c00|(1<<size)<<16|uint32(ins.B)<<5|a, fmt.Sprintf("dup V%d.%s, %s%d", a, suffix, gpr, ins.B))
	case ir.OpVExtract:
		imm5 := (uint32(ins.Imm) << (size + 1)) | 1<<size
		word := uint32(0x0e003c00)
		gpr := "W"
		lane := map[uint8]string{8: "B", 16: "H", 32: "S", 64: "D"}[ins.Bits]
		if ins.Bits == 64 {
			word |= 1 << 30
			gpr = "X"
		}
		emit(word|imm5<<16|b<<5|uint32(ins.A), fmt.Sprintf("umov %s%d, V%d.%s[%d]", gpr, ins.A, b, lane, ins.Imm))
	case ir.OpVInsert, ir.OpVInsertZero:
		copyVector(a, b)
		imm5 := (uint32(ins.Imm) << (size + 1)) | 1<<size
		gpr := "W"
		if ins.Bits == 64 {
			gpr = "X"
		}
		lane := map[uint8]string{8: "B", 16: "H", 32: "S", 64: "D"}[ins.Bits]
		emit(0x4e001c00|imm5<<16|uint32(ins.C)<<5|a, fmt.Sprintf("ins V%d.%s[%d], %s%d", a, lane, ins.Imm, gpr, ins.C))
	case ir.OpVCompare:
		if ins.Bits != 8 || ins.Imm != uint64(ir.BranchEqual) {
			return nil, machine.ErrUnsupportedOperation
		}
		emit(0x6e208c00|c<<16|b<<5|a, fmt.Sprintf("cmeq V%d.16B, V%d.16B, V%d.16B", a, b, c))
	case ir.OpVMaskBits:
		// Periodic bit weights, constructed without an aligned literal pool.
		for _, part := range ImmediatePlan(0x8040201008040201) {
			base := uint32(0xd2800000)
			if part.Op == "movk" {
				base = 0xf2800000
			} else if part.Op == "movn" {
				base = 0x92800000
			}
			emit(base|uint32(part.Shift/16)<<21|uint32(part.Imm)<<5|16, fmt.Sprintf("%s X16, #%d, lsl #%d", part.Op, part.Imm, part.Shift))
		}
		emit(0x4e080c00|16<<5|30, "dup V30.2D, X16")
		emit(0x4e201c00|30<<16|b<<5|30, fmt.Sprintf("and V30.16B, V%d.16B, V30.16B", b))
		emit(0x6e004000|30<<16|30<<5|31, "ext V31.16B, V30.16B, V30.16B, #8")
		emit(0x4e003800|31<<16|30<<5|30, "zip1 V30.16B, V30.16B, V31.16B")
		emit(0x4e71b800|30<<5|30, "addv H30, V30.8H")
		emit(0x0e023c00|30<<5|uint32(ins.A), fmt.Sprintf("umov W%d, V30.H[0]", ins.A))
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
		base, mnemonic := uint32(0x4e208400), "add"
		switch ins.Arithmetic {
		case ir.OpSub:
			base, mnemonic = 0x6e208400, "sub"
		case ir.OpMul:
			if ins.Bits == 64 {
				return nil, machine.ErrUnsupportedOperation
			}
			base, mnemonic = 0x4e209c00, "mul"
		case ir.OpAnd:
			base, mnemonic = 0x4e201c00, "and"
			size = 0
			suffix = "16B"
		case ir.OpOr:
			base, mnemonic = 0x4ea01c00, "orr"
			size = 0
			suffix = "16B"
		case ir.OpXor:
			base, mnemonic = 0x6e201c00, "eor"
			size = 0
			suffix = "16B"
		}
		emit(base|size<<22|c<<16|b<<5|a, fmt.Sprintf("%s V%d.%s, V%d.%s, V%d.%s", mnemonic, a, suffix, b, suffix, c, suffix))
	default:
		return nil, machine.ErrUnsupportedOperation
	}
	return plan, nil
}

// Vector visits an allocated NEON integer operation.
func (e *ByteAssembler) Vector(ins machine.Instruction) error {
	plan, err := vectorPlan(ins, e.stack, e.goHomes)
	if err != nil {
		return err
	}
	for _, n := range plan {
		e.words = append(e.words, n.word)
	}
	return nil
}

// Vector renders the identical NEON expansion as text.
func (e *TextAssembler) Vector(ins machine.Instruction) error {
	plan, err := vectorPlan(ins, e.stack, e.goHomes)
	if err != nil {
		return err
	}
	for _, n := range plan {
		e.line("%s", n.text)
	}
	return nil
}

// VectorSize measures the expansion before physical PC assignment.
func VectorSize(ins machine.Instruction, stack abi.StackPractice, homes int) int {
	p, err := vectorPlan(ins, stack, homes)
	if err != nil {
		return 0
	}
	return len(p) * 4
}
