package compile

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

// appendCompareSet selects OpCompare dst = a cond b at width. The target
// compares one register: a wider result is the zero extension of a
// register-width one. On a 32-bit target a 64-bit comparison combines word
// comparisons, equality as (lo^lo')|(hi^hi') == 0 and an order as
// hiStrict | (hiEqual & loUnsigned), and a signed 8- or 16-bit one compares
// its operands sign-extended to 32 bits, since 386 compares bytes only in
// AL..BL.
func appendCompareSet(program *machine.SelectedProgram, dst, a, b int, condition ir.BranchCondition, width, registerBits uint8) {
	value := func(bits uint8) int {
		id := len(program.Values)
		words, _ := regalloc.IntegerRegisterWords(bits, registerBits)
		program.Values = append(program.Values, machine.SelectedValue{Words: words, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	emit := func(ins machine.SelectedInstruction) { program.Instructions = append(program.Instructions, ins) }
	out := dst
	if program.Values[dst].Words > 1 {
		out = value(registerBits)
	}
	compare := func(dst int, condition ir.BranchCondition, width uint8, a, b int) {
		emit(machine.SelectedInstruction{Op: machine.SelectedCompareSet, Bits: width, Dst: dst, A: a, B: b, Imm: uint64(condition)})
	}
	binary := func(operation ir.Opcode, a, b int) int {
		id := value(registerBits)
		emit(machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: operation, Bits: registerBits, Dst: id, A: a, B: b})
		return id
	}
	switch {
	case registerBits == 32 && width == 64:
		low := func(x int) int {
			id := value(32)
			emit(machine.SelectedInstruction{Op: machine.SelectedNarrow, Dst: id, A: x, Imm: 32})
			return id
		}
		high := func(x int) int {
			shifted := value(64)
			emit(machine.SelectedInstruction{Op: machine.SelectedShift, Arithmetic: ir.OpShiftRight, Bits: 64, Dst: shifted, A: x, Imm: 32})
			return low(shifted)
		}
		aLow, aHigh, bLow, bHigh := low(a), high(a), low(b), high(b)
		if condition == ir.BranchEqual || condition == ir.BranchNotEqual {
			zero := value(32)
			program.Values[zero].Constant = true
			emit(machine.SelectedInstruction{Op: machine.SelectedConst, Dst: zero})
			compare(out, condition, 32, binary(ir.OpOr, binary(ir.OpXor, aLow, bLow), binary(ir.OpXor, aHigh, bHigh)), zero)
			break
		}
		strict, unsigned := strictCondition(condition), unsignedCondition(condition)
		highStrict, highEqual, lowOrder := value(32), value(32), value(32)
		compare(highStrict, strict, 32, aHigh, bHigh)
		compare(highEqual, ir.BranchEqual, 32, aHigh, bHigh)
		compare(lowOrder, unsigned, 32, aLow, bLow)
		emit(machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: ir.OpOr, Bits: 32, Dst: out, A: highStrict, B: binary(ir.OpAnd, highEqual, lowOrder)})
	case registerBits == 32 && width < 32 && machine.ComparisonFor(condition) == machine.CompareSigned:
		extend := func(x int) int {
			wide, id := value(64), value(32)
			emit(machine.SelectedInstruction{Op: machine.SelectedSignExtend, Dst: wide, A: x, Imm: uint64(width)})
			emit(machine.SelectedInstruction{Op: machine.SelectedNarrow, Dst: id, A: wide, Imm: 32})
			return id
		}
		compare(out, condition, 32, extend(a), extend(b))
	default:
		compare(out, condition, width, a, b)
	}
	if out != dst {
		emit(machine.SelectedInstruction{Op: machine.SelectedMove, Dst: dst, A: out})
	}
}

// strictCondition is the strict order of condition: Less for Less and
// LessEqual, Greater for Greater and GreaterEqual, and likewise unsigned.
func strictCondition(condition ir.BranchCondition) ir.BranchCondition {
	switch condition {
	case ir.BranchLessEqual:
		return ir.BranchLess
	case ir.BranchGreaterEqual:
		return ir.BranchGreater
	case ir.BranchBelowEqual:
		return ir.BranchBelow
	case ir.BranchAboveEqual:
		return ir.BranchAbove
	}
	return condition
}

// unsignedCondition is the unsigned order that matches condition.
func unsignedCondition(condition ir.BranchCondition) ir.BranchCondition {
	switch condition {
	case ir.BranchLess:
		return ir.BranchBelow
	case ir.BranchGreaterEqual:
		return ir.BranchAboveEqual
	case ir.BranchLessEqual:
		return ir.BranchBelowEqual
	case ir.BranchGreater:
		return ir.BranchAbove
	}
	return condition
}

// selectFlagsCompare reports whether an OpSelect whose condition is the
// single-use OpCompare compare can select from the comparison's flags: the
// target has a flag-consuming select, and its register-width compare decides
// the condition, because the operands are zero-extended or register-wide.
func selectFlagsCompare(architecture string, registerBits uint8, compare ir.Instruction, valueBits uint8) bool {
	switch architecture {
	case "amd64", "386", "arm64", "ppc64le":
	default:
		return false
	}
	width := selectedInstructionBits(compare)
	return width <= registerBits && valueBits <= registerBits &&
		(width == registerBits || machine.ComparisonFor(ir.BranchCondition(compare.Imm)) != machine.CompareSigned)
}

// appendReverseBytes selects dst = ReverseBytes(src) at width. A target
// without the native form, and a 64-bit value on a 32-bit target, use the
// word-and-mask fallbacks.
func appendReverseBytes(program *machine.SelectedProgram, dst, src int, width, registerBits uint8, native bool) {
	value := func(bits uint8) int {
		id := len(program.Values)
		words, _ := regalloc.IntegerRegisterWords(bits, registerBits)
		program.Values = append(program.Values, machine.SelectedValue{Words: words, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	emit := func(ins machine.SelectedInstruction) { program.Instructions = append(program.Instructions, ins) }
	if registerBits == 32 && width == 64 {
		// Reverse each word and swap them.
		low := func(x int) int {
			id := value(32)
			emit(machine.SelectedInstruction{Op: machine.SelectedNarrow, Dst: id, A: x, Imm: 32})
			return id
		}
		shifted := value(64)
		emit(machine.SelectedInstruction{Op: machine.SelectedShift, Arithmetic: ir.OpShiftRight, Bits: 64, Dst: shifted, A: src, Imm: 32})
		reversed := func(x int) int {
			id := value(32)
			emit(machine.SelectedInstruction{Op: machine.SelectedReverseBytes, Bits: 32, Dst: id, A: x})
			widened := value(64)
			emit(machine.SelectedInstruction{Op: machine.SelectedMove, Dst: widened, A: id})
			return widened
		}
		newLow, newHigh := reversed(low(shifted)), reversed(low(src))
		high := value(64)
		emit(machine.SelectedInstruction{Op: machine.SelectedShift, Arithmetic: ir.OpShiftLeft, Bits: 64, Dst: high, A: newHigh, Imm: 32})
		emit(machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: ir.OpOr, Bits: 64, Dst: dst, A: high, B: newLow})
		return
	}
	if native || registerBits == 32 {
		emit(machine.SelectedInstruction{Op: machine.SelectedReverseBytes, Bits: width, Dst: dst, A: src})
		return
	}
	// Swap adjacent bytes, then halfwords, then words, in 64-bit registers
	// holding the zero-extended value.
	constant := func(imm uint64) int {
		id := value(64)
		program.Values[id].Constant, program.Values[id].Imm = true, imm
		emit(machine.SelectedInstruction{Op: machine.SelectedConst, Dst: id, Imm: imm})
		return id
	}
	shift := func(operation ir.Opcode, x int, count uint8) int {
		id := value(64)
		emit(machine.SelectedInstruction{Op: machine.SelectedShift, Arithmetic: operation, Bits: 64, Dst: id, A: x, Imm: uint64(count)})
		return id
	}
	binary := func(operation ir.Opcode, x, y int) int {
		id := value(64)
		emit(machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: operation, Bits: 64, Dst: id, A: x, B: y})
		return id
	}
	// swap exchanges the count-bit fields that mask selects with their
	// neighbors above.
	swap := func(x int, count uint8, mask uint64) int {
		m := constant(mask)
		return binary(ir.OpOr, binary(ir.OpAnd, shift(ir.OpShiftRight, x, count), m), shift(ir.OpShiftLeft, binary(ir.OpAnd, x, m), count))
	}
	x := src
	switch width {
	case 16:
		x = binary(ir.OpOr, shift(ir.OpShiftRight, src, 8), binary(ir.OpAnd, shift(ir.OpShiftLeft, src, 8), constant(0xff00)))
	case 32:
		x = swap(src, 8, 0x00ff00ff)
		x = binary(ir.OpOr, shift(ir.OpShiftRight, x, 16), binary(ir.OpAnd, shift(ir.OpShiftLeft, x, 16), constant(0xffff0000)))
	default:
		x = swap(swap(src, 8, 0x00ff00ff00ff00ff), 16, 0x0000ffff0000ffff)
		x = binary(ir.OpOr, shift(ir.OpShiftRight, x, 32), shift(ir.OpShiftLeft, x, 32))
	}
	emit(machine.SelectedInstruction{Op: machine.SelectedMove, Dst: dst, A: x})
}
