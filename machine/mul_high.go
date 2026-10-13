package machine

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/regalloc"
)

// appendMulHighFallback forms a double-width unsigned product without a
// target high-product instruction. The 64-bit case uses four 32-bit products.
func appendMulHighFallback(program *SelectedProgram, dst, a, b int, width, registerBits uint8) {
	value := func(constant bool, imm uint64) int {
		id := len(program.Values)
		words, _ := regalloc.IntegerRegisterWords(64, registerBits)
		program.Values = append(program.Values, SelectedValue{Words: words, Constant: constant, Imm: imm, Param: -1, EntrySlot: -1, EntryReg: -1})
		if constant {
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedConst, Dst: id, Imm: imm})
		}
		return id
	}
	binary := func(op ir.Opcode, a, b int) int {
		id := value(false, 0)
		program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedBinary, Arithmetic: op, Bits: 64, Dst: id, A: a, B: b})
		return id
	}
	shift := func(a int, count uint8) int {
		id := value(false, 0)
		program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedShift, Arithmetic: ir.OpShiftRight, Bits: 64, Dst: id, A: a, Imm: uint64(count)})
		return id
	}
	if width < 64 {
		widen := func(source int) int {
			id := value(false, 0)
			program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedMove, Dst: id, A: source})
			return id
		}
		product := binary(ir.OpMul, widen(a), widen(b))
		result := shift(product, width)
		program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedNarrow, Dst: dst, A: result, Imm: uint64(width)})
		return
	}
	mask := value(true, 0xffffffff)
	a0, a1 := binary(ir.OpAnd, a, mask), shift(a, 32)
	b0, b1 := binary(ir.OpAnd, b, mask), shift(b, 32)
	p0, p1 := binary(ir.OpMul, a0, b0), binary(ir.OpMul, a0, b1)
	p2, p3 := binary(ir.OpMul, a1, b0), binary(ir.OpMul, a1, b1)
	middle := binary(ir.OpAdd, binary(ir.OpAdd, shift(p0, 32), binary(ir.OpAnd, p1, mask)), binary(ir.OpAnd, p2, mask))
	result := binary(ir.OpAdd, binary(ir.OpAdd, binary(ir.OpAdd, p3, shift(p1, 32)), shift(p2, 32)), shift(middle, 32))
	program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedMove, Dst: dst, A: result})
}
