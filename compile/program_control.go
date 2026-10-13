package compile

import "github.com/moriyoshi/nyanjit/ir"

func (b *loweringInput) controlSegment(step ir.ControlStep, predicate *ir.PredicateExpr) builderSegment {
	ops := make([]instruction, 0, step.End-step.Start+1)
	for _, ins := range b.code[step.Start:step.End] {
		switch ins.Op {
		case ir.OpCarryAdd, ir.OpBorrowSub, ir.OpMulHigh, ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy, opAdd, opSub, opMul, opAnd, opOr, opXor, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, opAddScaled, opStore64, opStore8:
			ins.A -= step.Start
			ins.B -= step.Start
		case opSelect:
			ins.A -= step.Start
			ins.B -= step.Start
			ins.C -= step.Start
		case ir.OpAddWithCarry, ir.OpSubWithBorrow, ir.OpFMA, ir.OpFProductError:
			ins.A -= step.Start
			ins.B -= step.Start
			ins.C -= step.Start
		case ir.OpSet, ir.OpLeadingZeros, ir.OpTrailingZeros, ir.OpFConvert, ir.OpFSqrt, opLoad64, opLoad8, opShiftRight, opShiftLeft, opShiftRightArithmetic, opRotateLeft, opRotateRight, opCastUnsigned, opSignExtend:
			ins.A -= step.Start
		case ir.OpScopeEnter, opSyscall, opCallC, opCallGo:
			ins.Args = append([]int(nil), ins.Args...)
			for i := range ins.Args {
				ins.Args[i] -= step.Start
			}
		}
		ops = append(ops, ins)
	}
	leaves := append([]scopeEvent(nil), b.leaves[step.LeaveStart:step.LeaveEnd]...)
	for i := range leaves {
		leaves[i].Enter = max(0, leaves[i].Enter-step.Start)
		leaves[i].Position -= step.Start
		leaves[i].Hidden = append([]int(nil), leaves[i].Hidden...)
		leaves[i].Exports = append([]int(nil), leaves[i].Exports...)
		for j := range leaves[i].Hidden {
			leaves[i].Hidden[j] -= step.Start
		}
		for j := range leaves[i].Exports {
			leaves[i].Exports[j] -= step.Start
		}
	}
	result := step.Result - step.Start
	var resultParts []int
	if len(step.ResultParts) != 0 {
		resultParts = make([]int, len(step.ResultParts))
		for i, id := range step.ResultParts {
			resultParts[i] = id - step.Start
		}
	}
	if step.Term == ir.TermJump {
		if len(step.Args) != 0 {
			result = step.Args[0] - step.Start
		} else {
			result = len(ops)
			ops = append(ops, instruction{Op: opConst, Bits: 8})
		}
	}
	exports := make([]int, 0, len(step.Values)+len(step.Args)+len(step.OtherArgs))
	seen := make(map[int]bool)
	addExport := func(id int) {
		if !seen[id] {
			seen[id] = true
			exports = append(exports, id-step.Start)
		}
	}
	if step.Term == ir.TermBranch {
		preserveResult := !ir.PredicateUsesLeftValue(predicate, step.Result)
		for _, id := range step.Values {
			if id == step.Result && !preserveResult && len(step.Args)+len(step.OtherArgs) == 0 {
				continue
			}
			addExport(id)
		}
	}
	for _, id := range step.Args {
		addExport(id)
	}
	for _, id := range step.OtherArgs {
		addExport(id)
	}
	direct := step.Term == ir.TermJump && len(step.Args) != 0
	unusedLeaves := true
	for _, leave := range leaves {
		if len(leave.Exports) != 0 {
			unusedLeaves = false
			break
		}
	}
	skip := step.Term == ir.TermJump && len(step.Args) == 0 && step.Start == step.End && unusedLeaves && step.Arity == 0
	return builderSegment{start: step.Start, ops: ops, leaves: leaves, arity: step.Arity, result: result, resultParts: resultParts, exports: exports, bare: direct || step.Term == ir.TermBranch && len(step.Args)+len(step.OtherArgs) == 0, direct: direct, skip: skip}
}

func lowerControlProgram(b *loweringInput, options CompileOptions, kind emissionKind) (emissionArtifact, EmissionReport, error) {
	if b == nil || b.control == nil || len(b.control.Steps) == 0 {
		return emissionArtifact{}, EmissionReport{}, ErrInvalidFunction
	}
	e := NewEmitter()
	e.source, e.homePlan = b.source, b.homes
	if b.homes != nil {
		e.reservedSlots = b.homes.ReservedSlots
	}
	incoming := make([]int, len(b.control.Labels))
	firstLabel := Label(-1)
	for _, step := range b.control.Steps {
		if step.IsLabel {
			if firstLabel < 0 {
				firstLabel = Label(step.Label)
			}
			continue
		}
		if step.Term == ir.TermJump || step.Term == ir.TermBranch {
			incoming[step.To]++
		}
		if step.Term == ir.TermBranch {
			incoming[step.Other]++
		}
	}
	for label, count := range b.control.Params {
		if count > 0 && (incoming[label] == 0 || Label(label) == firstLabel) {
			return emissionArtifact{}, EmissionReport{}, ErrInvalidFunction
		}
	}
	validEdge := func(ids []int, label Label) bool {
		if label < 0 || int(label) >= len(b.control.ParamBits) || len(ids) != len(b.control.ParamBits[label]) {
			return false
		}
		for i, id := range ids {
			if id < 0 || id >= len(b.code) || instructionBits(b.code[id]) != b.control.ParamBits[label][i] {
				return false
			}
		}
		return true
	}
	e.labels = append([]bool(nil), b.control.Labels...)
	e.labelSlots = make([][]int, len(b.control.Labels))
	e.labelRegisters = make([][]int, len(b.control.Labels))
	e.labelWords = make([][]uint8, len(b.control.Labels))
	e.labelBits = b.control.ParamBits
	target := newMachineTarget(options)
	if target == nil {
		return emissionArtifact{}, EmissionReport{}, ErrUnsupported
	}
	registers := allocationRegisters(target, b.homes, options.Stack)
	fpLabels := floatingLabelParams(b.source)
	fpRegisters := floatRegisters(target.Architecture())
	if b.homes != nil {
		available := []physReg{}
		for _, r := range registers {
			if !b.homes.Reserved[r] {
				available = append(available, r)
			}
		}
		registers = available
		available = nil
		for _, r := range fpRegisters {
			if !b.homes.Reserved[r] {
				available = append(available, r)
			}
		}
		fpRegisters = available
	}
	for label, count := range b.control.Params {
		registerIndex := 0
		fpIndex := 0
		for i := 0; i < count; i++ {
			width := b.control.ParamBits[label][i]
			words := uint8((int(width) + int(target.RegisterBits()) - 1) / int(target.RegisterBits()))
			e.labelWords[label] = append(e.labelWords[label], words)
			if words == 1 && target.RegisterBits() == 64 && fpLabels != nil && fpLabels[label][i] && fpIndex < len(fpRegisters) {
				e.labelRegisters[label] = append(e.labelRegisters[label], int(fpRegisters[fpIndex]))
				e.labelSlots[label] = append(e.labelSlots[label], -1)
				fpIndex++
			} else if words == 1 && registerIndex < len(registers) {
				e.labelRegisters[label] = append(e.labelRegisters[label], int(registers[registerIndex]))
				e.labelSlots[label] = append(e.labelSlots[label], -1)
				registerIndex++
			} else {
				if e.reservedSlots+int(words) > maxSpillSlots {
					return emissionArtifact{}, EmissionReport{}, ErrBackendLimit
				}
				e.labelRegisters[label] = append(e.labelRegisters[label], -1)
				e.labelSlots[label] = append(e.labelSlots[label], e.reservedSlots)
				e.reservedSlots += int(words)
			}
		}
	}
	if options.PreferCompactLoops && target.Architecture() == "amd64" && (b.homes == nil || !b.homes.Reserved[1]) {
		labelOrder := make(map[int]int)
		for order, step := range b.control.Steps {
			if step.IsLabel {
				labelOrder[step.Label] = order
			}
		}
		pinned := make(map[int]bool)
		for order, step := range b.control.Steps {
			predicate := step.Predicate
			if step.IsLabel || step.Term != ir.TermBranch || predicate == nil ||
				predicate.Kind != ir.PredicateCompare || predicate.WithValue || predicate.Immediate != 0 ||
				predicate.Condition != ir.BranchNotEqual || labelOrder[step.To] >= order || pinned[step.To] ||
				predicate.Value < 0 || predicate.Value >= len(b.code) {
				continue
			}
			decrement := b.code[predicate.Value]
			if decrement.Op != opSub || instructionBits(decrement) != 64 || decrement.B < 0 ||
				decrement.B >= len(b.code) || b.code[decrement.B].Op != opConst || b.code[decrement.B].Imm != 1 {
				continue
			}
			for arg, value := range step.Args {
				if value != predicate.Value || arg >= len(e.labelRegisters[step.To]) ||
					arg >= len(b.control.ParamBits[step.To]) || b.control.ParamBits[step.To][arg] != 64 {
					continue
				}
				old := e.labelRegisters[step.To][arg]
				if old < 0 {
					continue
				}
				for other, reg := range e.labelRegisters[step.To] {
					if reg == 1 {
						e.labelRegisters[step.To][other] = old
					}
				}
				e.labelRegisters[step.To][arg] = 1
				pinned[step.To] = true
				break
			}
		}
	}
	rebaseArgs := func(ids []int, start int) []int {
		out := make([]int, len(ids))
		for i, id := range ids {
			out[i] = id - start
		}
		return out
	}
	currentLabel := Label(-1)
	for _, step := range b.control.Steps {
		if step.IsLabel {
			currentLabel = Label(step.Label)
			e.steps = append(e.steps, emissionStep{kind: stepLabel, label: Label(step.Label)})
			continue
		}
		var predicate *ir.PredicateExpr
		if step.Term == ir.TermBranch {
			predicate = foldControlConstants(step.Predicate, b.code)
		}
		segment := b.controlSegment(step, predicate)
		if predicate != nil {
			// Constants consumed as compare immediates do not need edge spill homes.
			used := map[int]bool{}
			var visit func(*ir.PredicateExpr)
			visit = func(p *ir.PredicateExpr) {
				if p == nil {
					return
				}
				if p.Kind == ir.PredicateCompare {
					used[p.Value] = true
					if p.WithValue {
						used[p.Other] = true
					}
					if p.HasAnchor {
						used[p.Anchor] = true
					}
				}
				visit(p.Left)
				visit(p.Right)
			}
			visit(predicate)
			for _, id := range step.Args {
				used[id] = true
			}
			for _, id := range step.OtherArgs {
				used[id] = true
			}
			out := segment.exports[:0]
			for _, id := range segment.exports {
				global := id + step.Start
				ins := b.code[global]
				if ins.Op != opConst || ins.Symbol != "" || used[global] {
					out = append(out, id)
				}
			}
			segment.exports = out
		}
		// Paired 386 predicates compare both words after the fragment has
		// finished. Export their operands, including the fragment result.
		if target.RegisterBits() == 32 && predicate != nil {
			seen := map[int]bool{}
			for _, id := range segment.exports {
				seen[id] = true
			}
			var visit func(*ir.PredicateExpr)
			visit = func(p *ir.PredicateExpr) {
				if p == nil {
					return
				}
				if p.Kind == ir.PredicateCompare && instructionBits(b.code[p.Value]) == 64 {
					ids := []int{p.Value}
					if p.WithValue {
						ids = append(ids, p.Other)
					}
					for _, id := range ids {
						id -= step.Start
						if !seen[id] {
							segment.exports = append(segment.exports, id)
							seen[id] = true
						}
					}
				}
				visit(p.Left)
				visit(p.Right)
			}
			visit(predicate)
		}
		for i := range segment.ops {
			if segment.ops[i].Op == opLabelParam {
				index := int(segment.ops[i].Imm)
				if currentLabel < 0 || index >= len(e.labelSlots[currentLabel]) {
					return emissionArtifact{}, EmissionReport{}, ErrInvalidFunction
				}
				segment.ops[i].EntryReg = e.labelRegisters[currentLabel][index]
				if slot := e.labelSlots[currentLabel][index]; slot >= 0 {
					segment.ops[i].Imm = uint64(slot)
				}
			}
		}
		e.steps = append(e.steps, emissionStep{kind: stepBuilder, segment: &segment})
		switch step.Term {
		case ir.TermJump:
			if !validEdge(step.Args, Label(step.To)) {
				return emissionArtifact{}, EmissionReport{}, ErrInvalidFunction
			}
			e.steps = append(e.steps, emissionStep{kind: stepJump, label: Label(step.To), args: rebaseArgs(step.Args, step.Start)})
		case ir.TermBranch:
			if !validEdge(step.Args, Label(step.To)) || !validEdge(step.OtherArgs, Label(step.Other)) {
				return emissionArtifact{}, EmissionReport{}, ErrInvalidFunction
			}
			e.steps = append(e.steps, emissionStep{
				kind: stepBranch, label: Label(step.To), other: Label(step.Other),
				predicate: ir.RebasePredicate(predicate, step.Start),
				args:      rebaseArgs(step.Args, step.Start), otherArgs: rebaseArgs(step.OtherArgs, step.Start),
			})
		case ir.TermReturn:
			e.steps = append(e.steps, emissionStep{kind: stepExit})
		}
	}
	return e.lower(options, kind)
}

// foldControlConstants preserves the caller's predicate tree and turns literal
// RHS values into immediates before deciding which edge operands need homes.
func foldControlConstants(p *ir.PredicateExpr, ops []instruction) *ir.PredicateExpr {
	p = ir.RebasePredicate(p, 0)
	var visit func(*ir.PredicateExpr)
	visit = func(node *ir.PredicateExpr) {
		if node == nil {
			return
		}
		if node.Kind == ir.PredicateCompare && node.WithValue && !node.HasAnchor && node.Condition <= BranchAbove && node.Other >= 0 && node.Other < len(ops) {
			literal := ops[node.Other]
			if literal.Op == opConst && literal.Symbol == "" {
				node.WithValue = false
				node.Immediate = int64(literal.Imm)
				node.Other = -1
			}
		}
		visit(node.Left)
		visit(node.Right)
	}
	visit(p)
	return p
}
