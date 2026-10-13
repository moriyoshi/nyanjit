package irconform

import "github.com/moriyoshi/nyanjit/ir"

// block is one label and its segment in a hand-written control program.
// Operands are absolute value indices; end holds the terminator fields.
type block struct {
	label int
	code  []ir.Instruction
	end   ir.ControlStep
}

// controlProgram assembles blocks in order. paramBits gives each label's
// parameter widths.
func controlProgram(paramBits [][]uint8, blocks ...block) func() (*ir.SealedProgram, error) {
	return func() (*ir.SealedProgram, error) {
		p := &ir.Program{Result: -1, Control: &ir.Control{}}
		c := p.Control
		for _, bits := range paramBits {
			c.Labels = append(c.Labels, true)
			c.LabelScopes = append(c.LabelScopes, 0)
			c.Params = append(c.Params, len(bits))
			c.ParamBits = append(c.ParamBits, bits)
		}
		for _, b := range blocks {
			step := b.end
			step.Start = len(p.Instructions)
			p.Instructions = append(p.Instructions, b.code...)
			step.End = len(p.Instructions)
			for _, ins := range b.code {
				if ins.Op == ir.OpParam {
					step.Arity++
				}
			}
			p.Arity += step.Arity
			c.Steps = append(c.Steps, ir.ControlStep{IsLabel: true, Label: b.label}, step)
		}
		return ir.Seal(p)
	}
}

func compareValues(a ir.BranchCondition, x, y int) *ir.PredicateExpr {
	return &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: a, Value: x, Other: y, WithValue: true}
}

// crossBlockCases use values in later blocks their definitions dominate,
// without passing them as label arguments.
func crossBlockCases() []Case {
	param := ir.Instruction{Op: ir.OpParam, Bits: 64}
	jump := func(to int, args ...int) ir.ControlStep { return ir.ControlStep{Term: ir.TermJump, To: to, Args: args} }
	ret := func(result int) ir.ControlStep { return ir.ControlStep{Term: ir.TermReturn, Result: result} }
	return []Case{
		{Name: crossBlockPrefix + "straight", Inputs: unary(64), Build: controlProgram([][]uint8{{}, {}},
			block{0, []ir.Instruction{param, {Op: ir.OpConst, Bits: 64, Imm: 10}, {Op: ir.OpAdd, A: 0, B: 1}}, jump(1)},
			block{1, []ir.Instruction{{Op: ir.OpMul, A: 2, B: 2}, {Op: ir.OpXor, A: 3, B: 0}}, ret(4)},
		)},
		{Name: crossBlockPrefix + "diamond", Inputs: unary(64), Build: controlProgram([][]uint8{{}, {}, {}, {64}},
			block{0, []ir.Instruction{param, {Op: ir.OpConst, Bits: 64, Imm: 3}},
				ir.ControlStep{Term: ir.TermBranch, To: 1, Other: 2, Predicate: compareValues(ir.BranchLess, 0, 1), Values: []int{0, 1}, Result: 1}},
			block{1, []ir.Instruction{{Op: ir.OpAdd, A: 0, B: 0}}, jump(3, 2)},
			block{2, []ir.Instruction{{Op: ir.OpSub, A: 0, B: 1}}, jump(3, 3)},
			block{3, []ir.Instruction{{Op: ir.OpLabelParam, Bits: 64}, {Op: ir.OpXor, A: 4, B: 1}}, ret(5)},
		)},
		{Name: crossBlockPrefix + "loop", Inputs: [][]uint64{{1, 0}, {5, 3}, {7, 10}, {^uint64(0), 4}}, Build: controlProgram([][]uint8{{}, {64, 64}, {}, {}},
			block{0, []ir.Instruction{param, param, {Op: ir.OpConst, Bits: 64, Imm: 3}, {Op: ir.OpMul, A: 0, B: 2}, {Op: ir.OpConst, Bits: 64}}, jump(1, 4, 4)},
			block{1, []ir.Instruction{{Op: ir.OpLabelParam, Bits: 64}, {Op: ir.OpLabelParam, Bits: 64, Imm: 1}, {Op: ir.OpOr, A: 1, B: 1}},
				ir.ControlStep{Term: ir.TermBranch, To: 2, Other: 3, Predicate: compareValues(ir.BranchBelow, 5, 7), Values: []int{5, 7}, Result: 7}},
			block{2, []ir.Instruction{{Op: ir.OpAdd, A: 6, B: 3}, {Op: ir.OpConst, Bits: 64, Imm: 1}, {Op: ir.OpAdd, A: 5, B: 9}}, jump(1, 10, 8)},
			block{3, []ir.Instruction{{Op: ir.OpAdd, A: 6, B: 3}}, ret(11)},
		)},
		{Name: crossBlockPrefix + "predicate", Inputs: binary(64), Build: controlProgram([][]uint8{{}, {}, {}, {}},
			block{0, []ir.Instruction{param, param}, jump(1)},
			block{1, []ir.Instruction{{Op: ir.OpConst, Bits: 64}},
				ir.ControlStep{Term: ir.TermBranch, To: 2, Other: 3, Predicate: compareValues(ir.BranchLess, 0, 1), Values: []int{0, 1}, Result: 2}},
			block{2, []ir.Instruction{{Op: ir.OpSub, A: 0, B: 1}}, ret(3)},
			block{3, []ir.Instruction{{Op: ir.OpSub, A: 1, B: 0}}, ret(4)},
		)},
		{Name: crossBlockPrefix + "edge-argument", Inputs: unary(64), Build: controlProgram([][]uint8{{}, {}, {64}},
			block{0, []ir.Instruction{param, {Op: ir.OpConst, Bits: 64, Imm: 5}, {Op: ir.OpAdd, A: 0, B: 1}}, jump(1)},
			block{1, []ir.Instruction{{Op: ir.OpConst, Bits: 64, Imm: 1}}, jump(2, 2)},
			block{2, []ir.Instruction{{Op: ir.OpLabelParam, Bits: 64}, {Op: ir.OpAdd, A: 4, B: 3}}, ret(5)},
		)},
		{Name: crossBlockPrefix + "return", Inputs: unary(64), Build: controlProgram([][]uint8{{}, {}},
			block{0, []ir.Instruction{param, {Op: ir.OpMul, A: 0, B: 0}}, jump(1)},
			block{1, nil, ret(1)},
		)},
	}
}
