package arm64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

type vectorConcrete struct {
	word uint32
	text string
}

// The longest NEON expansion is the ten-word byte-mask extraction. Keep the
// complete plan on the stack, including optional listing text.
type concretePlan struct {
	parts [12]vectorConcrete
	n     int
}

func vectorPlan(ins machine.Instruction, stack abi.StackPractice, homes int, text bool) (concretePlan, error) {
	if !validVectorOperands(&ins) || !validSlot(ins.Slot, homes) {
		return concretePlan{}, machine.ErrUnsupportedOperation
	}
	format := func(f string, args ...any) string {
		if !text {
			return ""
		}
		return fmt.Sprintf(f, args...)
	}
	var plan concretePlan
	emit := func(word uint32) { plan.parts[plan.n].word = word; plan.n++ }
	vr := func(r backend.Register) uint32 { return uint32(backend.VectorNumber(r)) }
	a, b, c := vr(ins.A), vr(ins.B), vr(ins.C)
	copyVector := func(dst, src uint32) {
		if dst != src {
			emit(0x4ea01c00 | src<<16 | src<<5 | dst)
			if text {
				plan.parts[plan.n-1].text = format("mov V%d.16B, V%d.16B", dst, src)
			}
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
			return concretePlan{}, machine.ErrUnsupportedOperation
		}
		word, mnemonic := uint32(0x3d800000), "str"
		if ins.Op == machine.OpVectorReload {
			word, mnemonic = 0x3dc00000, "ldr"
		}
		baseName := ""
		if text {
			baseName = format("X%d", base)
		}
		if base == 31 {
			baseName = "SP"
		}
		emit(word | uint32(offset/16)<<10 | base<<5 | a)
		if text {
			plan.parts[plan.n-1].text = format("%s Q%d, [%s, #%d]", mnemonic, a, baseName, offset)
		}
		return plan, nil
	}
	size, suffix, _ := vectorLanes(ins.Bits)
	switch ins.Arithmetic {
	case op.OpVLoad:
		emit(0x3dc00000 | uint32(ins.B)<<5 | a)
		if text {
			plan.parts[plan.n-1].text = format("ldr Q%d, [X%d]", a, ins.B)
		}
	case op.OpVStore:
		emit(0x3d800000 | uint32(ins.A)<<5 | b)
		if text {
			plan.parts[plan.n-1].text = format("str Q%d, [X%d]", b, ins.A)
		}
	case op.OpVZero:
		emit(0x4f00e400 | a)
		if text {
			plan.parts[plan.n-1].text = format("movi V%d.16B, #0", a)
		}
	case op.OpVSplat:
		gpr := "W"
		if ins.Bits == 64 {
			gpr = "X"
		}
		emit(0x4e000c00 | (1<<size)<<16 | uint32(ins.B)<<5 | a)
		if text {
			plan.parts[plan.n-1].text = format("dup V%d.%s, %s%d", a, suffix, gpr, ins.B)
		}
	case op.OpVExtract:
		imm5 := (uint32(ins.Imm) << (size + 1)) | 1<<size
		word := uint32(0x0e003c00)
		gpr := "W"
		_, _, lane := vectorLanes(ins.Bits)
		if ins.Bits == 64 {
			word |= 1 << 30
			gpr = "X"
		}
		emit(word | imm5<<16 | b<<5 | uint32(ins.A))
		if text {
			plan.parts[plan.n-1].text = format("umov %s%d, V%d.%s[%d]", gpr, ins.A, b, lane, ins.Imm)
		}
	case op.OpVInsert, op.OpVInsertZero:
		copyVector(a, b)
		imm5 := (uint32(ins.Imm) << (size + 1)) | 1<<size
		gpr := "W"
		if ins.Bits == 64 {
			gpr = "X"
		}
		_, _, lane := vectorLanes(ins.Bits)
		emit(0x4e001c00 | imm5<<16 | uint32(ins.C)<<5 | a)
		if text {
			plan.parts[plan.n-1].text = format("ins V%d.%s[%d], %s%d", a, lane, ins.Imm, gpr, ins.C)
		}
	case op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight:
		if ins.Bits != 32 {
			return concretePlan{}, machine.ErrUnsupportedOperation
		}
		count := ins.Imm
		rotate := ins.Arithmetic == op.OpRotateLeft || ins.Arithmetic == op.OpRotateRight
		if rotate {
			count %= 32
		}
		if !rotate && count >= 32 {
			emit(0x4f00e400 | a)
			if text {
				plan.parts[plan.n-1].text = format("movi V%d.16B, #0", a)
			}
			break
		}
		if count == 0 {
			copyVector(a, b)
			break
		}
		left := ins.Arithmetic == op.OpShiftLeft || ins.Arithmetic == op.OpRotateLeft
		shift := func(dst, src uint32, left bool, count uint64) {
			base, immediate, mnemonic := uint32(0x6f000400), uint32(64-count), "ushr"
			if left {
				base, immediate, mnemonic = 0x4f005400, uint32(32+count), "shl"
			}
			emit(base | immediate<<16 | src<<5 | dst)
			if text {
				plan.parts[plan.n-1].text = format("%s V%d.4S, V%d.4S, #%d", mnemonic, dst, src, count)
			}
		}
		if rotate && a != b {
			shift(a, b, left, count)
			// Insert the complementary bits while retaining the first half.
			base, immediate, mnemonic := uint32(0x6f005400), uint32(64-count), "sli"
			if left {
				base, immediate, mnemonic = 0x6f004400, uint32(32+count), "sri"
			}
			emit(base | immediate<<16 | b<<5 | a)
			if text {
				plan.parts[plan.n-1].text = format("%s V%d.4S, V%d.4S, #%d", mnemonic, a, b, 32-count)
			}
			break
		}
		// V30 is reserved for expansion. Computing this half first permits
		// destination/input aliasing without losing the original lanes.
		if rotate {
			shift(30, b, !left, 32-count)
		}
		shift(a, b, left, count)
		if rotate {
			emit(0x4ea01c00 | 30<<16 | a<<5 | a)
			if text {
				plan.parts[plan.n-1].text = format("orr V%d.16B, V%d.16B, V30.16B", a, a)
			}
		}
	case op.OpVCompare:
		if ins.Bits != 8 || ins.Imm != uint64(op.BranchEqual) {
			return concretePlan{}, machine.ErrUnsupportedOperation
		}
		emit(0x6e208c00 | c<<16 | b<<5 | a)
		if text {
			plan.parts[plan.n-1].text = format("cmeq V%d.16B, V%d.16B, V%d.16B", a, b, c)
		}
	case op.OpVMaskBits:
		// Periodic bit weights, constructed without an aligned literal pool.
		parts := immediatePlan(0x8040201008040201)
		for _, part := range parts.parts[:parts.n] {
			emit(part.word(16))
			if text {
				plan.parts[plan.n-1].text = format("%s X16, #%d, lsl #%d", part.op, part.imm, part.shift)
			}
		}
		emit(0x4e080c00 | 16<<5 | 30)
		if text {
			plan.parts[plan.n-1].text = "dup V30.2D, X16"
		}
		emit(0x4e201c00 | 30<<16 | b<<5 | 30)
		if text {
			plan.parts[plan.n-1].text = format("and V30.16B, V%d.16B, V30.16B", b)
		}
		emit(0x6e004000 | 30<<16 | 30<<5 | 31)
		if text {
			plan.parts[plan.n-1].text = "ext V31.16B, V30.16B, V30.16B, #8"
		}
		emit(0x4e003800 | 31<<16 | 30<<5 | 30)
		if text {
			plan.parts[plan.n-1].text = "zip1 V30.16B, V30.16B, V31.16B"
		}
		emit(0x4e71b800 | 30<<5 | 30)
		if text {
			plan.parts[plan.n-1].text = "addv H30, V30.8H"
		}
		emit(0x0e023c00 | 30<<5 | uint32(ins.A))
		if text {
			plan.parts[plan.n-1].text = format("umov W%d, V30.H[0]", ins.A)
		}
	case op.OpAdd, op.OpSub, op.OpMul, op.OpAnd, op.OpOr, op.OpXor:
		base, mnemonic := uint32(0x4e208400), "add"
		switch ins.Arithmetic {
		case op.OpSub:
			base, mnemonic = 0x6e208400, "sub"
		case op.OpMul:
			if ins.Bits == 64 {
				return concretePlan{}, machine.ErrUnsupportedOperation
			}
			base, mnemonic = 0x4e209c00, "mul"
		case op.OpAnd:
			base, mnemonic = 0x4e201c00, "and"
			size = 0
			suffix = "16B"
		case op.OpOr:
			base, mnemonic = 0x4ea01c00, "orr"
			size = 0
			suffix = "16B"
		case op.OpXor:
			base, mnemonic = 0x6e201c00, "eor"
			size = 0
			suffix = "16B"
		}
		emit(base | size<<22 | c<<16 | b<<5 | a)
		if text {
			plan.parts[plan.n-1].text = format("%s V%d.%s, V%d.%s, V%d.%s", mnemonic, a, suffix, b, suffix, c, suffix)
		}
	default:
		return concretePlan{}, machine.ErrUnsupportedOperation
	}
	return plan, nil
}

// Vector visits an allocated NEON integer operation.
func (e *ByteAssembler) Vector(ins machine.Instruction) error {
	if !e.require((validInstructionRegisters(&ins))) {
		return machine.ErrUnsupportedOperation
	}

	plan, err := vectorPlan(ins, e.stack, e.goHomes, false)
	if err != nil {
		return err
	}
	for _, n := range plan.parts[:plan.n] {
		e.words = append(e.words, n.word)
	}
	return nil
}

// Vector renders the identical NEON expansion as text.
func (e *TextAssembler) Vector(ins machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.Vector(ins) }); err != nil {
		return err
	}
	plan, err := vectorPlan(ins, e.stack, e.goHomes, true)
	if err != nil {
		return err
	}
	for _, n := range plan.parts[:plan.n] {
		e.line("%s", n.text)
	}
	return nil
}

// VectorSize measures the expansion before physical PC assignment.
func VectorSize(ins machine.Instruction, stack abi.StackPractice, homes int) int {
	if !validVectorOperands(&ins) || !validSlot(ins.Slot, homes) {
		return 0
	}
	// Sizing only needs word counts, not concrete words or listing strings.
	// Keep the encoding-plan constraints here; the parity test covers every
	// operation, width, lane and destination/input alias arrangement.
	switch ins.Op {
	case machine.OpVectorMove:
		if ins.A == ins.B {
			return 0
		}
		return 4
	case machine.OpVectorSpill, machine.OpVectorReload:
		offset := ins.Slot * 8
		if stack == abi.StackPlatform {
			offset = (ins.Slot + 1 + homes) * 8
		}
		if offset%16 != 0 || offset/16 > 4095 {
			return 0
		}
		return 4
	}
	switch ins.Arithmetic {
	case op.OpVLoad, op.OpVStore, op.OpVZero, op.OpVSplat, op.OpVExtract,
		op.OpAdd, op.OpSub, op.OpAnd, op.OpOr, op.OpXor:
		return 4
	case op.OpVInsert, op.OpVInsertZero:
		if ins.A == ins.B {
			return 4
		}
		return 8
	case op.OpMul:
		if ins.Bits == 64 {
			return 0
		}
		return 4
	case op.OpVCompare:
		if ins.Bits != 8 || ins.Imm != uint64(op.BranchEqual) {
			return 0
		}
		return 4
	case op.OpVMaskBits:
		return (immediatePlan(0x8040201008040201).n + 6) * 4
	case op.OpShiftLeft, op.OpShiftRight, op.OpRotateLeft, op.OpRotateRight:
		if ins.Bits != 32 {
			return 0
		}
		count := ins.Imm
		rotate := ins.Arithmetic == op.OpRotateLeft || ins.Arithmetic == op.OpRotateRight
		if rotate {
			count %= 32
		}
		if !rotate && count >= 32 {
			return 4
		}
		if count == 0 {
			if ins.A == ins.B {
				return 0
			}
			return 4
		}
		if !rotate {
			return 4
		}
		if ins.A != ins.B {
			return 8
		}
		return 12
	default:
		return 0
	}
}

// vectorLanes is the size field, the arrangement of a full 128-bit vector
// and the element name of a lane width.
func vectorLanes(bits uint8) (uint32, string, string) {
	switch bits {
	case 8:
		return 0, "16B", "B"
	case 16:
		return 1, "8H", "H"
	case 32:
		return 2, "4S", "S"
	case 64:
		return 3, "2D", "D"
	}
	return 0, "", ""
}
