package compile

import (
	"math/bits"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

// Targets without a native register-count operation receive a portable barrel
// network after selection. The Builder stays independent of count-register and
// addressing constraints.
func appendShiftByFallback(program *machine.SelectedProgram, dst, value, count int, operation ir.Opcode, width, countWidth, registerBits uint8) {
	newValue := func(width uint8) int {
		id := len(program.Values)
		words, _ := regalloc.IntegerRegisterWords(width, registerBits)
		program.Values = append(program.Values, machine.SelectedValue{Words: words, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	constant := func(width uint8, value uint64) int {
		id := newValue(width)
		program.Values[id].Constant, program.Values[id].Imm = true, value
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedConst, Dst: id, Imm: value})
		return id
	}
	var shift func(int, ir.Opcode, uint8, uint8) int
	shift = func(source int, operation ir.Opcode, width, amount uint8) int {
		if width < 32 && (operation == ir.OpRotateLeft || operation == ir.OpRotateRight) {
			leftCount, rightCount := amount, width-amount
			if operation == ir.OpRotateRight {
				leftCount, rightCount = rightCount, leftCount
			}
			left := shift(source, ir.OpShiftLeft, width, leftCount)
			right := shift(source, ir.OpShiftRight, width, rightCount)
			id := newValue(width)
			program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: ir.OpOr, Bits: width, Dst: id, A: left, B: right})
			return id
		}
		id := newValue(width)
		raw := id
		if width < registerBits {
			raw = newValue(registerBits)
		}
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedShift, Arithmetic: operation, Bits: width, Dst: raw, A: source, Imm: uint64(amount)})
		if raw != id {
			program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedNarrow, Dst: id, A: raw, Imm: uint64(width)})
		}
		return id
	}
	conditionWord := func(source, sourceWidth int) int {
		if sourceWidth <= int(registerBits) {
			return source
		}
		id := newValue(registerBits)
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedNarrow, Dst: id, A: source, Imm: uint64(registerBits)})
		return id
	}
	result := value
	for amount := uint8(1); amount < width; amount <<= 1 {
		mask := constant(countWidth, uint64(amount))
		condition := newValue(countWidth)
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: ir.OpAnd, Bits: countWidth, Dst: condition, A: count, B: mask})
		condition = conditionWord(condition, int(countWidth))
		shifted := shift(result, operation, width, amount)
		next := newValue(width)
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedSelect, Dst: next, A: condition, B: shifted, C: result})
		result = next
	}
	if operation == ir.OpRotateLeft || operation == ir.OpRotateRight {
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedMove, Dst: dst, A: result})
		return
	}
	tooLarge := shift(count, ir.OpShiftRight, countWidth, uint8(bits.TrailingZeros8(width)))
	if countWidth > registerBits {
		// The high half of a 64-bit count is also evidence of saturation.
		high := shift(count, ir.OpShiftRight, countWidth, registerBits)
		low, high := conditionWord(tooLarge, int(countWidth)), conditionWord(high, int(countWidth))
		combined := newValue(registerBits)
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedBinary, Arithmetic: ir.OpOr, Bits: registerBits, Dst: combined, A: low, B: high})
		tooLarge = combined
	}
	saturated := 0
	if operation == ir.OpShiftRightArithmetic {
		saturated = shift(value, operation, width, width-1)
	} else {
		saturated = constant(width, 0)
	}
	program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: machine.SelectedSelect, Dst: dst, A: tooLarge, B: saturated, C: result})
}
