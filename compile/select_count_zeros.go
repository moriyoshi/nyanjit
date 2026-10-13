package compile

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

func appendCountZerosFallback(p *machine.SelectedProgram, dst, source int, op ir.Opcode, width, registerBits uint8) {
	value := func() int {
		id := len(p.Values)
		w, _ := regalloc.IntegerRegisterWords(width, registerBits)
		p.Values = append(p.Values, machine.SelectedValue{Words: w, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	constant := func(v uint64) int {
		id := value()
		p.Values[id].Constant = true
		p.Values[id].Imm = v
		p.Instructions = append(p.Instructions, machine.SelectedInstruction{Op: machine.SelectedConst, Dst: id, Imm: v})
		return id
	}
	binary := func(op ir.Opcode, a, b int) int {
		id := value()
		p.Instructions = append(p.Instructions, machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: op, Bits: width, Dst: id, A: a, B: b})
		if width < registerBits {
			n := value()
			p.Instructions = append(p.Instructions, machine.SelectedInstruction{Op: machine.SelectedNarrow, Dst: n, A: id, Imm: uint64(width)})
			return n
		}
		return id
	}
	shift := func(op ir.Opcode, a int, count uint8) int {
		id := value()
		p.Instructions = append(p.Instructions, machine.SelectedInstruction{Op: machine.SelectedShift, Arithmetic: op, Bits: width, Dst: id, A: a, Imm: uint64(count)})
		if width < registerBits {
			n := value()
			p.Instructions = append(p.Instructions, machine.SelectedInstruction{Op: machine.SelectedNarrow, Dst: n, A: id, Imm: uint64(width)})
			return n
		}
		return id
	}
	selectValue := func(c, a, b int) int {
		id := value()
		p.Instructions = append(p.Instructions, machine.SelectedInstruction{Op: machine.SelectedSelect, Dst: id, A: c, B: a, C: b})
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
	p.Instructions = append(p.Instructions, machine.SelectedInstruction{Op: machine.SelectedMove, Dst: dst, A: result})
}
