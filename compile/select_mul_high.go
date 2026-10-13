package compile

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

// appendMulHighFallback forms a double-width unsigned product without a
// target high-product instruction. The 64-bit case uses four 32-bit products.
func appendMulHighFallback(program *machine.SelectedProgram, dst, a, b int, width, registerBits uint8) {
	value := func(constant bool, imm uint64) int {
		id := len(program.Values)
		words, _ := regalloc.IntegerRegisterWords(64, registerBits)
		program.Values = append(program.Values, machine.SelectedValue{Words: words, Constant: constant, Imm: imm, Param: -1, EntrySlot: -1, EntryReg: -1})
		if constant {
			program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedConst, Dst: id, Imm: imm})
		}
		return id
	}
	binary := func(op ir.Opcode, a, b int) int {
		id := value(false, 0)
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: op, Bits: 64, Dst: id, A: a, B: b})
		return id
	}
	shift := func(a int, count uint8) int {
		id := value(false, 0)
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedShift, Arithmetic: ir.OpShiftRight, Bits: 64, Dst: id, A: a, Imm: uint64(count)})
		return id
	}
	if width < 64 {
		widen := func(source int) int {
			id := value(false, 0)
			program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedMove, Dst: id, A: source})
			return id
		}
		product := binary(ir.OpMul, widen(a), widen(b))
		result := shift(product, width)
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedNarrow, Dst: dst, A: result, Imm: uint64(width)})
		return
	}
	mask := value(true, 0xffffffff)
	a0, a1 := binary(ir.OpAnd, a, mask), shift(a, 32)
	b0, b1 := binary(ir.OpAnd, b, mask), shift(b, 32)
	p0, p1 := binary(ir.OpMul, a0, b0), binary(ir.OpMul, a0, b1)
	p2, p3 := binary(ir.OpMul, a1, b0), binary(ir.OpMul, a1, b1)
	middle := binary(ir.OpAdd, binary(ir.OpAdd, shift(p0, 32), binary(ir.OpAnd, p1, mask)), binary(ir.OpAnd, p2, mask))
	result := binary(ir.OpAdd, binary(ir.OpAdd, binary(ir.OpAdd, p3, shift(p1, 32)), shift(p2, 32)), shift(middle, 32))
	program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedMove, Dst: dst, A: result})
}

// appendMulHighSignedFallback forms the high half of a signed product
// without a target signed high-product instruction. A narrow product is
// exact in one register-width multiply (or a 64-bit one) of the operands,
// sign-extended by a left and an arithmetic right shift; the 64-bit case
// corrects the unsigned high half, subtracting each operand where the
// other is negative.
func appendMulHighSignedFallback(program *machine.SelectedProgram, dst, a, b int, width, registerBits uint8, nativeUnsigned bool) {
	value := func(bits uint8) int {
		id := len(program.Values)
		words, _ := regalloc.IntegerRegisterWords(bits, registerBits)
		program.Values = append(program.Values, machine.SelectedValue{Words: words, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	emit := func(bits uint8, ins machine.SelectedInstruction) int {
		ins.Dst = value(bits)
		program.Instructions = append(program.Instructions, ins)
		return ins.Dst
	}
	shift := func(bits uint8, operation ir.Opcode, source int, count uint8) int {
		return emit(bits, machine.SelectedInstruction{Op: machine.SelectedShift, Arithmetic: operation, Bits: bits, A: source, Imm: uint64(count)})
	}
	if width < 64 {
		productBits := uint8(64)
		if 2*width <= registerBits {
			productBits = registerBits
		}
		extend := func(source int) int {
			return shift(productBits, ir.OpShiftRightArithmetic, shift(productBits, ir.OpShiftLeft, source, productBits-width), productBits-width)
		}
		product := emit(productBits, machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: ir.OpMul, Bits: productBits, A: extend(a), B: extend(b)})
		high := shift(productBits, ir.OpShiftRightArithmetic, product, width)
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedNarrow, Dst: dst, A: high, Imm: uint64(width)})
		return
	}
	high := value(64)
	if nativeUnsigned {
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedMulHigh, Bits: 64, Dst: high, A: a, B: b})
	} else {
		appendMulHighFallback(program, high, a, b, 64, registerBits)
	}
	correction := func(sign, other int) int {
		mask := shift(64, ir.OpShiftRightArithmetic, sign, 63)
		return emit(64, machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: ir.OpAnd, Bits: 64, A: mask, B: other})
	}
	high = emit(64, machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: ir.OpSub, Bits: 64, A: high, B: correction(a, b)})
	high = emit(64, machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: ir.OpSub, Bits: 64, A: high, B: correction(b, a)})
	program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedMove, Dst: dst, A: high})
}
