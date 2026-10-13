package compile

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

func appendWordCarryFallback(program *machine.SelectedProgram, dst, a, b, c int, operation ir.Opcode, width, registerBits uint8) {
	value := func(constant bool, imm uint64) int {
		id := len(program.Values)
		words, _ := regalloc.IntegerRegisterWords(width, registerBits)
		program.Values = append(program.Values, machine.SelectedValue{Words: words, Constant: constant, Imm: imm, Param: -1, EntrySlot: -1, EntryReg: -1})
		if constant {
			program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedConst, Dst: id, Imm: imm})
		}
		return id
	}
	binary := func(op ir.Opcode, a, b int) int {
		id := value(false, 0)
		raw := id
		if width < registerBits {
			raw = value(false, 0)
		}
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: op, Bits: width, Dst: raw, A: a, B: b})
		if raw != id {
			program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedNarrow, Dst: id, A: raw, Imm: uint64(width)})
		}
		return id
	}
	if operation == ir.OpAddWithCarry || operation == ir.OpSubWithBorrow {
		op := ir.OpAdd
		if operation == ir.OpSubWithBorrow {
			op = ir.OpSub
		}
		bit := binary(ir.OpAnd, c, value(true, 1))
		result := binary(op, binary(op, a, b), bit)
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedMove, Dst: dst, A: result})
		return
	}
	all := value(true, ^uint64(0)>>(64-width))
	carryBits := 0
	if operation == ir.OpCarryAdd {
		sum := binary(ir.OpAdd, a, b)
		carryBits = binary(ir.OpOr, binary(ir.OpAnd, a, b), binary(ir.OpAnd, binary(ir.OpOr, a, b), binary(ir.OpXor, sum, all)))
	} else {
		diff := binary(ir.OpSub, a, b)
		carryBits = binary(ir.OpOr, binary(ir.OpAnd, binary(ir.OpXor, a, all), b), binary(ir.OpAnd, binary(ir.OpXor, binary(ir.OpXor, a, b), all), diff))
	}
	program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedShift, Arithmetic: ir.OpShiftRight, Bits: width, Dst: dst, A: carryBits, Imm: uint64(width - 1)})
}
