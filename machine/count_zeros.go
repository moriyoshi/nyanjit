package machine

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/regalloc"
)

func appendCountZerosFallback(p *SelectedProgram, dst, source int, op ir.Opcode, width, registerBits uint8) {
	value := func() int {
		id := len(p.Values)
		w, _ := regalloc.IntegerRegisterWords(width, registerBits)
		p.Values = append(p.Values, SelectedValue{Words: w, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	constant := func(v uint64) int {
		id := value()
		p.Values[id].Constant = true
		p.Values[id].Imm = v
		p.Instructions = append(p.Instructions, SelectedInstruction{Op: SelectedConst, Dst: id, Imm: v})
		return id
	}
	binary := func(op ir.Opcode, a, b int) int {
		id := value()
		p.Instructions = append(p.Instructions, SelectedInstruction{Op: SelectedBinary, Arithmetic: op, Bits: width, Dst: id, A: a, B: b})
		if width < registerBits {
			n := value()
			p.Instructions = append(p.Instructions, SelectedInstruction{Op: SelectedNarrow, Dst: n, A: id, Imm: uint64(width)})
			return n
		}
		return id
	}
	shift := func(op ir.Opcode, a int, count uint8) int {
		id := value()
		p.Instructions = append(p.Instructions, SelectedInstruction{Op: SelectedShift, Arithmetic: op, Bits: width, Dst: id, A: a, Imm: uint64(count)})
		if width < registerBits {
			n := value()
			p.Instructions = append(p.Instructions, SelectedInstruction{Op: SelectedNarrow, Dst: n, A: id, Imm: uint64(width)})
			return n
		}
		return id
	}
	selectValue := func(c, a, b int) int {
		id := value()
		p.Instructions = append(p.Instructions, SelectedInstruction{Op: SelectedSelect, Dst: id, A: c, B: a, C: b})
		return id
	}
	x, n := source, constant(0)
	for step := width / 2; step > 0; step /= 2 {
		condition := 0
		candidate := 0
		if op == ir.OpLeadingZeros {
			condition = shift(ir.OpShiftRight, x, width-step)
			candidate = shift(ir.OpShiftLeft, x, step)
		} else {
			condition = binary(ir.OpAnd, x, constant((uint64(1)<<step)-1))
			candidate = shift(ir.OpShiftRight, x, step)
		}
		next := binary(ir.OpAdd, n, constant(uint64(step)))
		n = selectValue(condition, n, next)
		x = selectValue(condition, x, candidate)
	}
	result := selectValue(source, n, constant(uint64(width)))
	p.Instructions = append(p.Instructions, SelectedInstruction{Op: SelectedMove, Dst: dst, A: result})
}
