package compile

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// segmentArena backs the relocated copies controlSegment makes. The segments
// live until the control program is lowered, so their copies share chunks.
type segmentArena struct {
	ops    []instruction
	leaves []scopeEvent
	ints   []int
}

func (b *loweringInput) controlSegment(step ir.ControlStep, predicate *ir.PredicateExpr, arena *segmentArena) builderSegment {
	ops := carve(&arena.ops, step.End-step.Start+1)
	for _, ins := range b.code[step.Start:step.End] {
		switch ins.Op {
		case ir.OpCarryAdd, ir.OpBorrowSub, ir.OpMulHigh, ir.OpMulHighSigned, ir.OpFCompare, ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned, ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy, opAdd, opSub, opMul, opAnd, opOr, opXor, ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, opAddScaled, opStore64, opStore8, opStore16, opStore32:
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
		case ir.OpSet, ir.OpLeadingZeros, ir.OpTrailingZeros, ir.OpFConvert, ir.OpFSqrt, ir.OpFFromSigned, ir.OpFFromUnsigned, ir.OpFToSigned, ir.OpFToUnsigned, opLoad64, opLoad8, opLoad16, opLoad32, opShiftRight, opShiftLeft, opShiftRightArithmetic, opRotateLeft, opRotateRight, opCastUnsigned, opSignExtend:
			ins.A -= step.Start
		case ir.OpScopeEnter, ir.OpScopeLeave, opSyscall, opCallC, opCallGo:
			ins.Args = carveCopy(&arena.ints, ins.Args)
			for i := range ins.Args {
				ins.Args[i] -= step.Start
			}
		}
		ops = append(ops, ins)
	}
	leaves := carveCopy(&arena.leaves, b.leaves[step.LeaveStart:step.LeaveEnd])
	for i := range leaves {
		leaves[i].Enter = max(0, leaves[i].Enter-step.Start)
		leaves[i].Position -= step.Start
		leaves[i].Hidden = carveCopy(&arena.ints, leaves[i].Hidden)
		leaves[i].Exports = carveCopy(&arena.ints, leaves[i].Exports)
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
	exports := carve(&arena.ints, len(step.Values)+len(step.Args)+len(step.OtherArgs))
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

func lowerControlProgram(b *loweringInput, options compileOptions, kind emissionKind) (emissionArtifact, abi.EmissionReport, error) {
	if b == nil || b.control == nil || len(b.control.Steps) == 0 {
		return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
	}
	e := NewEmitter()
	e.source, e.homePlan, e.facts, e.scratch = b.source, b.homes, b.facts, b.scratch
	if b.homes != nil {
		e.reservedSlots = b.homes.ReservedSlots
	}
	e.homeSlots = e.reservedSlots
	var localControl controlBuffers
	buffers := &localControl
	if b.scratch != nil {
		buffers = &b.scratch.control
	}
	incoming := resetBufferTracked(&buffers.incoming, &buffers.history.incoming, len(b.control.Labels))
	e.steps = appendBufferTracked(&buffers.steps, &buffers.history.steps, len(b.control.Steps)*2)
	defer func() { buffers.steps = e.steps }()
	// Labels bound before the same segment alias one block, entered through
	// any of them; incoming counts the edges into each block.
	blockOf := make([]int, len(b.control.Labels))
	block := 0
	for _, step := range b.control.Steps {
		if step.IsLabel {
			blockOf[step.Label] = block
		} else {
			block++
		}
	}
	for _, step := range b.control.Steps {
		if step.IsLabel {
			continue
		}
		if step.Term == ir.TermJump || step.Term == ir.TermBranch {
			incoming[blockOf[step.To]]++
		}
		if step.Term == ir.TermBranch {
			incoming[blockOf[step.Other]]++
		}
	}
	for label, count := range b.control.Params {
		if count > 0 && (incoming[blockOf[label]] == 0 || blockOf[label] == 0) {
			return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
		}
	}
	validEdge := func(ids []int, label ir.Label) bool {
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
	e.labels = append(appendBufferTracked(&buffers.labels, &buffers.history.labels, len(b.control.Labels)), b.control.Labels...)
	buffers.labels = e.labels
	e.labelSlots = resetBufferTracked(&buffers.labelSlots, &buffers.history.labelSlots, len(b.control.Labels))
	e.labelRegisters = resetBufferTracked(&buffers.labelRegisters, &buffers.history.labelRegisters, len(b.control.Labels))
	e.labelWords = resetBufferTracked(&buffers.labelWords, &buffers.history.labelWords, len(b.control.Labels))
	e.labelBits = b.control.ParamBits
	target := newMachineTarget(options)
	if target == nil {
		return emissionArtifact{}, abi.EmissionReport{}, ErrUnsupported
	}
	registers := allocationRegisters(target, b.homes, options.Stack)
	fpLabels := b.facts
	if fpLabels == nil {
		fpLabels = newProgramFacts(b.source)
	}
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
	// Labels bound before the same segment alias one block, whose
	// parameter reads use a single list of homes; validation requires their
	// parameters to match, so later aliases share the first label's homes.
	aliasOf := make([]int, len(b.control.Labels))
	for label := range aliasOf {
		aliasOf[label] = label
	}
	first := -1
	for _, step := range b.control.Steps {
		switch {
		case !step.IsLabel:
			first = -1
		case first < 0:
			first = step.Label
		default:
			aliasOf[step.Label] = first
		}
	}
	for label, count := range b.control.Params {
		if alias := aliasOf[label]; alias != label {
			continue
		}
		registerIndex := 0
		fpIndex := 0
		for i := 0; i < count; i++ {
			width := b.control.ParamBits[label][i]
			words := uint8((int(width) + int(target.RegisterBits()) - 1) / int(target.RegisterBits()))
			e.labelWords[label] = append(e.labelWords[label], words)
			if floating := fpLabels.LabelFloating(int(label)); i < len(floating) && floating[i] && fpIndex < len(fpRegisters) {
				e.labelRegisters[label] = append(e.labelRegisters[label], int(fpRegisters[fpIndex]))
				e.labelSlots[label] = append(e.labelSlots[label], -1)
				fpIndex++
			} else if words == 1 && registerIndex < len(registers) {
				e.labelRegisters[label] = append(e.labelRegisters[label], int(registers[registerIndex]))
				e.labelSlots[label] = append(e.labelSlots[label], -1)
				registerIndex++
			} else {
				if e.reservedSlots+int(words) > maxSpillSlots {
					return emissionArtifact{}, abi.EmissionReport{}, spillStorageLimitError(e.reservedSlots+int(words), e.reservedSlots, maxSpillSlots)
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
				predicate.Condition != ir.BranchNotEqual || labelOrder[step.To] >= order || pinned[aliasOf[step.To]] ||
				predicate.Value < 0 || predicate.Value >= len(b.code) {
				continue
			}
			decrement := b.code[predicate.Value]
			if decrement.Op != opSub || instructionBits(decrement) != 64 || decrement.B < 0 ||
				decrement.B >= len(b.code) || b.code[decrement.B].Op != opConst || b.code[decrement.B].Imm != 1 {
				continue
			}
			to := aliasOf[step.To]
			for arg, value := range step.Args {
				if value != predicate.Value || arg >= len(e.labelRegisters[to]) ||
					arg >= len(b.control.ParamBits[to]) || b.control.ParamBits[to][arg] != 64 {
					continue
				}
				old := e.labelRegisters[to][arg]
				if old < 0 {
					continue
				}
				for other, reg := range e.labelRegisters[to] {
					if reg == 1 {
						e.labelRegisters[to][other] = old
					}
				}
				e.labelRegisters[to][arg] = 1
				pinned[to] = true
				if e.compactPins == nil {
					e.compactPins = make([]int, len(b.control.Labels))
					for i := range e.compactPins {
						e.compactPins[i] = -1
					}
				}
				e.compactPins[to] = arg
				break
			}
		}
	}
	for label, alias := range aliasOf {
		if alias != label {
			e.labelWords[label] = append(e.labelWords[label], e.labelWords[alias]...)
			e.labelRegisters[label] = append(e.labelRegisters[label], e.labelRegisters[alias]...)
			e.labelSlots[label] = append(e.labelSlots[label], e.labelSlots[alias]...)
			if e.compactPins != nil {
				e.compactPins[label] = e.compactPins[alias]
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
	var arena segmentArena
	opCount, segmentCount := 0, 0
	for _, step := range b.control.Steps {
		if !step.IsLabel {
			opCount += step.End - step.Start + 1
			segmentCount++
		}
	}
	arena.ops = make([]instruction, 0, opCount)
	// Sized exactly, so appending never moves a segment a step points to.
	segments := make([]builderSegment, 0, segmentCount)
	currentLabel := ir.Label(-1)
	for _, step := range b.control.Steps {
		if step.IsLabel {
			currentLabel = ir.Label(step.Label)
			e.steps = append(e.steps, emissionStep{kind: stepLabel, label: ir.Label(step.Label)})
			continue
		}
		var predicate *ir.PredicateExpr
		if step.Term == ir.TermBranch {
			var err error
			predicate, err = foldControlConstants(step.Predicate, b.code)
			if err != nil {
				return emissionArtifact{}, abi.EmissionReport{}, err
			}
		}
		// Steps point into segments, which holds every segment in one array.
		segments = append(segments, b.controlSegment(step, predicate, &arena))
		segment := &segments[len(segments)-1]
		if predicate != nil {
			// Constants consumed as compare immediates do not need edge spill homes.
			used := map[int]bool{}
			for p, err := range predicateComparisons(predicate) {
				if err != nil {
					return emissionArtifact{}, abi.EmissionReport{}, err
				}
				used[p.Value] = true
				if p.WithValue {
					used[p.Other] = true
				}
				if p.HasAnchor {
					used[p.Anchor] = true
				}
			}
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
			for p, err := range predicateComparisons(predicate) {
				if err != nil {
					return emissionArtifact{}, abi.EmissionReport{}, err
				}
				if instructionBits(b.code[p.Value]) == 64 {
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
			}
		}
		if currentLabel >= 0 && len(e.labelSlots[currentLabel]) != 0 {
			segment.params = carve(&arena.ints, len(e.labelSlots[currentLabel]))[:len(e.labelSlots[currentLabel])]
			for i := range segment.params {
				segment.params[i] = -1
			}
		}
		for i := range segment.ops {
			if segment.ops[i].Op == opLabelParam {
				index := int(segment.ops[i].Imm)
				if currentLabel < 0 || index >= len(e.labelSlots[currentLabel]) {
					return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
				}
				if segment.params[index] >= 0 {
					segment.repeated = true
				}
				segment.params[index] = i
				segment.ops[i].EntryReg = e.labelRegisters[currentLabel][index]
				if slot := e.labelSlots[currentLabel][index]; slot >= 0 {
					segment.ops[i].Imm = uint64(slot)
				}
			}
		}
		e.steps = append(e.steps, emissionStep{kind: stepBuilder, segment: segment})
		switch step.Term {
		case ir.TermJump:
			if !validEdge(step.Args, ir.Label(step.To)) {
				return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
			}
			e.steps = append(e.steps, emissionStep{kind: stepJump, label: ir.Label(step.To), args: rebaseArgs(step.Args, step.Start)})
		case ir.TermBranch:
			if !validEdge(step.Args, ir.Label(step.To)) || !validEdge(step.OtherArgs, ir.Label(step.Other)) {
				return emissionArtifact{}, abi.EmissionReport{}, ErrInvalidFunction
			}
			e.steps = append(e.steps, emissionStep{
				kind: stepBranch, label: ir.Label(step.To), other: ir.Label(step.Other),
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
func foldControlConstants(p *ir.PredicateExpr, ops []instruction) (*ir.PredicateExpr, error) {
	p = ir.RebasePredicate(p, 0)
	for node, err := range predicateComparisons(p) {
		if err != nil {
			return nil, err
		}
		if node.WithValue && !node.HasAnchor && node.Condition <= ir.BranchAbove && node.Other >= 0 && node.Other < len(ops) {
			literal := ops[node.Other]
			if literal.Op == opConst && literal.Symbol == "" {
				node.WithValue = false
				node.Immediate = int64(literal.Imm)
				node.Other = -1
			}
		}
	}
	return p, nil
}
