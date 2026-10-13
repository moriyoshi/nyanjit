package compile

import (
	"fmt"
	"slices"

	"github.com/moriyoshi/nyanjit/ir"
)

// Lowering selects each block separately, so a value used outside its
// defining block reaches the use through label parameters. Validation has
// checked that each such definition dominates its uses; threadCrossBlockValues
// computes where the values are live, appends one parameter per live value to
// each block that needs it, and passes the values along every edge. Programs
// without cross-block uses are not rewritten, so their code is unchanged.
//
// Blocks unreachable from the entry never run. Their references to outer
// values, and the arguments they pass to reachable blocks, become zero
// constants of the right type.

// hasCrossBlockUses reports whether some block of p refers to a value defined
// in another block.
func hasCrossBlockUses(p *ir.Program) bool {
	if p.Control == nil {
		return false
	}
	var buffer [4]int
	outside := func(ids []int, start int) bool {
		for _, id := range ids {
			if id < start {
				return true
			}
		}
		return false
	}
	for _, step := range p.Control.Steps {
		if step.IsLabel {
			continue
		}
		for index := step.Start; index < step.End; index++ {
			if outside(p.Instructions[index].InputsInto(buffer[:0]), step.Start) {
				return true
			}
		}
		if outside(step.Values, step.Start) || outside(step.Args, step.Start) || outside(step.OtherArgs, step.Start) ||
			outside(step.ResultParts, step.Start) || step.Term != ir.TermJump && step.Term != ir.TermTailCall && step.Term != ir.TermSwitch && step.Result < step.Start ||
			step.Term == ir.TermSwitch && step.Selector < step.Start {
			return true
		}
		for _, exit := range p.ScopeExits[step.LeaveStart:step.LeaveEnd] {
			if outside(exit.Exports, step.Start) {
				return true
			}
		}
	}
	return false
}

// crossBlockBlock is one segment of the program being threaded.
type crossBlockBlock struct {
	step        int // control step index
	start, end  int // original instruction range
	succ        []int
	reachable   bool
	uses        []int // values from other blocks referred to here, sorted
	live        []int // live-in values; the parameters added to the block
	inserted    int   // instructions inserted at the block start
	zeros       []int // unreachable blocks: values replaced by zero constants, sorted
	zeroAt      []int // new index of each zero
	firstInsert int   // new index of the first inserted instruction
}

// threadCrossBlockValues returns p with cross-block uses threaded through label
// parameters. p is a validated program and is not modified.
func threadCrossBlockValues(p *ir.Program) (*ir.Program, error) {
	control := p.Control
	var labelBuffer [16]int
	labelBlock := labelBuffer[:0]
	if len(control.Labels) <= len(labelBuffer) {
		labelBlock = labelBuffer[:len(control.Labels)]
	} else {
		labelBlock = make([]int, len(control.Labels))
	}
	segments, edges := 0, 0
	for _, step := range control.Steps {
		if !step.IsLabel {
			segments++
			edges += 2 + len(step.Cases)
		}
	}
	var blockBuffer [16]crossBlockBlock
	blocks := blockBuffer[:0]
	if segments > len(blockBuffer) {
		blocks = make([]crossBlockBlock, 0, segments)
	}
	// The successor and predecessor lists share one array each.
	succArena := make([]int, 0, edges)
	var pending []int
	for index, step := range control.Steps {
		if step.IsLabel {
			pending = append(pending, step.Label)
			continue
		}
		for _, label := range pending {
			labelBlock[label] = len(blocks)
		}
		pending = pending[:0]
		blocks = append(blocks, crossBlockBlock{step: index, start: step.Start, end: step.End})
	}
	for b := range blocks {
		step := &control.Steps[blocks[b].step]
		switch step.Term {
		case ir.TermJump:
			blocks[b].succ = append(carve(&succArena, 1), labelBlock[step.To])
		case ir.TermBranch:
			blocks[b].succ = append(carve(&succArena, 2), labelBlock[step.To], labelBlock[step.Other])
		case ir.TermSwitch:
			seen := map[int]bool{labelBlock[step.To]: true}
			blocks[b].succ = append(carve(&succArena, 1+len(step.Cases)), labelBlock[step.To])
			for _, label := range step.Cases {
				if s := labelBlock[label]; !seen[s] {
					seen[s] = true
					blocks[b].succ = append(blocks[b].succ, s)
				}
			}
		}
	}
	// Reachability from the entry block.
	blocks[0].reachable = true
	for work := []int{0}; len(work) != 0; {
		b := work[len(work)-1]
		work = work[:len(work)-1]
		for _, s := range blocks[b].succ {
			if !blocks[s].reachable {
				blocks[s].reachable = true
				work = append(work, s)
			}
		}
	}
	// Values each block refers to from outside.
	var buffer [4]int
	for b := range blocks {
		block := &blocks[b]
		step := &control.Steps[block.step]
		add := func(ids ...int) {
			for _, id := range ids {
				if id < block.start {
					block.uses = append(block.uses, id)
				}
			}
		}
		for index := block.start; index < block.end; index++ {
			add(p.Instructions[index].InputsInto(buffer[:0])...)
		}
		add(step.Values...)
		add(step.Args...)
		add(step.OtherArgs...)
		add(step.ResultParts...)
		switch step.Term {
		case ir.TermJump, ir.TermTailCall:
		case ir.TermSwitch:
			add(step.Selector)
		default:
			add(step.Result)
		}
		for _, exit := range p.ScopeExits[step.LeaveStart:step.LeaveEnd] {
			add(exit.Exports...)
		}
		slices.Sort(block.uses)
		block.uses = slices.Compact(block.uses)
	}
	// Liveness, one value at a time: walk back from each reachable use to the
	// defining block, which dominates every block on the way. Values are
	// visited in ascending order, so live lists come out sorted. The work and
	// the output (one parameter per live block, one argument per edge into
	// it) are the same size, and the budget bounds both.
	var predecessorBuffer [16][]int
	var countBuffer [16]int
	preds, predCounts := predecessorBuffer[:0], countBuffer[:0]
	if len(blocks) <= len(predecessorBuffer) {
		preds, predCounts = predecessorBuffer[:len(blocks)], countBuffer[:len(blocks)]
	} else {
		preds, predCounts = make([][]int, len(blocks)), make([]int, len(blocks))
	}
	predTotal := 0
	for b := range blocks {
		for _, s := range blocks[b].succ {
			predCounts[s]++
			predTotal++
		}
	}
	predArena := make([]int, 0, predTotal)
	for b, n := range predCounts {
		preds[b] = carve(&predArena, n)
	}
	for b := range blocks {
		for _, s := range blocks[b].succ {
			preds[s] = append(preds[s], b)
		}
	}
	type use struct{ value, block int }
	var uses []use
	for b := range blocks {
		if blocks[b].reachable {
			for _, id := range blocks[b].uses {
				uses = append(uses, use{id, b})
			}
		}
	}
	slices.SortFunc(uses, func(x, y use) int { return x.value - y.value })
	budget := 8*len(p.Instructions) + 1<<16
	threaded := 0
	var markBuffer [16]int
	marked := markBuffer[:0]
	if len(blocks) <= len(markBuffer) {
		marked = markBuffer[:len(blocks)]
	} else {
		marked = make([]int, len(blocks))
	}
	for i := range marked {
		marked[i] = -1
	}
	var workBuffer [32]int
	work := workBuffer[:0]
	for i := 0; i < len(uses); {
		value, def := uses[i].value, sortSearchBlocks(blocks, uses[i].value)
		mark := func(b int) error {
			if b == def || marked[b] == value || !blocks[b].reachable {
				return nil
			}
			if b == 0 {
				return fmt.Errorf("value %d is used where its definition does not dominate: %w", value, ErrInvalidFunction)
			}
			marked[b] = value
			blocks[b].live = append(blocks[b].live, value)
			if threaded += 1 + len(preds[b]); threaded > budget {
				return &LimitError{Kind: LimitArguments, Needed: threaded, Limit: budget, Detail: "label parameters and arguments that carry values across blocks"}
			}
			work = append(work, b)
			return nil
		}
		for ; i < len(uses) && uses[i].value == value; i++ {
			if err := mark(uses[i].block); err != nil {
				return nil, err
			}
		}
		for len(work) != 0 {
			b := work[len(work)-1]
			work = work[:len(work)-1]
			for _, pred := range preds[b] {
				if err := mark(pred); err != nil {
					return nil, err
				}
			}
		}
	}
	// Every edge of a switch passes the same arguments, so its successors
	// take the union of their live values. Lowering gives a switch
	// successors of their own (see desugarSwitches), whose only
	// predecessor it is, so the extra parameters reach no other edge.
	for b := range blocks {
		if step := &control.Steps[blocks[b].step]; step.Term != ir.TermSwitch || !blocks[b].reachable {
			continue
		}
		var union []int
		for _, s := range blocks[b].succ {
			if len(preds[s]) != 1 {
				return nil, fmt.Errorf("switch successor block %d has %d predecessors: %w", s, len(preds[s]), ErrInvalidFunction)
			}
			union = append(union, blocks[s].live...)
		}
		slices.Sort(union)
		union = slices.Compact(union)
		for _, s := range blocks[b].succ {
			if threaded += len(union) - len(blocks[s].live); threaded > budget {
				return nil, &LimitError{Kind: LimitArguments, Needed: threaded, Limit: budget, Detail: "label parameters and arguments that carry values across blocks"}
			}
			blocks[s].live = append(blocks[s].live[:0:0], union...)
		}
	}
	// Unreachable blocks use zero constants for outer values and for the
	// parameters of their successors.
	// Only unreachable blocks need zeros, and masks only there a vector type.
	var maskVector map[uint16]ir.Type
	for b := range blocks {
		if !blocks[b].reachable {
			maskVector = make(map[uint16]ir.Type)
			break
		}
	}
	if maskVector != nil {
		for _, ins := range p.Instructions {
			if t := ins.ValueType(); t.Kind == ir.VectorKind {
				if _, ok := maskVector[t.Lanes]; !ok {
					maskVector[t.Lanes] = t
				}
			}
		}
	}
	zeroSize := func(id int) (int, error) {
		t := p.Instructions[id].ValueType()
		if t.Kind != ir.MaskKind {
			return 1, nil
		}
		if _, ok := maskVector[t.Lanes]; !ok {
			return 0, fmt.Errorf("no vector type for a %d-lane mask in unreachable code: %w", t.Lanes, ErrUnsupported)
		}
		return 2, nil
	}
	for b := range blocks {
		block := &blocks[b]
		if block.reachable {
			block.inserted = len(block.live)
			continue
		}
		zeros := append([]int(nil), block.uses...)
		for _, s := range block.succ {
			zeros = append(zeros, blocks[s].live...)
		}
		slices.Sort(zeros)
		zeros = slices.Compact(zeros)
		// Successor parameters defined in this block need no zero.
		zeros = slices.DeleteFunc(zeros, func(id int) bool { return id >= block.start && id < block.end })
		block.zeros = zeros
		for _, id := range zeros {
			size, err := zeroSize(id)
			if err != nil {
				return nil, err
			}
			block.inserted += size
		}
	}
	// New positions: block b's inserted instructions come first in its
	// segment, then its original instructions.
	var shiftBuffer [17]int
	shiftBefore := shiftBuffer[:0] // instructions inserted before block b's segment
	if len(blocks) < len(shiftBuffer) {
		shiftBefore = shiftBuffer[:len(blocks)+1]
	} else {
		shiftBefore = make([]int, len(blocks)+1)
	}
	for b := range blocks {
		shiftBefore[b+1] = shiftBefore[b] + blocks[b].inserted
	}
	blockOf := func(id int) int {
		return sortSearchBlocks(blocks, id)
	}
	newIndex := func(id int) int { return id + shiftBefore[blockOf(id)+1] }
	// local maps a value referred to in block b to its new index there.
	local := func(b, id int) int {
		block := &blocks[b]
		if id >= block.start && id < block.end {
			return id + shiftBefore[b+1] // newIndex(id): the segments are in order
		}
		if block.reachable {
			k, _ := slices.BinarySearch(block.live, id)
			return block.firstInsert + k
		}
		k, _ := slices.BinarySearch(block.zeros, id)
		return block.zeroAt[k]
	}
	out := &ir.Program{
		ParameterTypes: p.ParameterTypes, Finalizers: p.Finalizers, Arity: p.Arity, Result: p.Result, StackAreas: p.StackAreas,
		Instructions: make([]ir.Instruction, 0, len(p.Instructions)+shiftBefore[len(blocks)]),
		ScopeExits:   make([]ir.ScopeExit, len(p.ScopeExits)),
		Variables:    make([]ir.Variable, len(p.Variables)),
	}
	if p.Result >= 0 {
		out.Result = newIndex(p.Result)
	}
	// Label parameters.
	var paramBuffer [16]int
	labelParamBase := paramBuffer[:0] // original parameter count of each block
	if len(blocks) <= len(paramBuffer) {
		labelParamBase = paramBuffer[:len(blocks)]
	} else {
		labelParamBase = make([]int, len(blocks))
	}
	for label, b := range labelBlock {
		labelParamBase[b] = control.Params[label]
	}
	newControl := &ir.Control{
		Labels: append([]bool(nil), control.Labels...), LabelScopes: append([]int(nil), control.LabelScopes...),
		Params: append([]int(nil), control.Params...), ParamBits: make([][]uint8, len(control.Labels)),
		Steps: make([]ir.ControlStep, len(control.Steps)),
	}
	typed := len(control.ParamTypes) != 0
	for _, block := range blocks {
		for _, id := range block.live {
			if t := p.Instructions[id].ValueType(); t.Kind != ir.ScalarKind {
				typed = true
			}
		}
	}
	if typed {
		newControl.ParamTypes = make([][]ir.Type, len(control.Labels))
	}
	// The labels' lists share one array each.
	paramCount := 0
	for label := range control.Labels {
		paramCount += len(control.ParamBits[label]) + len(blocks[labelBlock[label]].live)
	}
	bitsArena := make([]uint8, 0, paramCount)
	var typeArena []ir.Type
	if typed {
		typeArena = make([]ir.Type, 0, paramCount)
	}
	for label := range control.Labels {
		if n := len(control.ParamBits[label]) + len(blocks[labelBlock[label]].live); n != 0 {
			newControl.ParamBits[label] = append(carve(&bitsArena, n), control.ParamBits[label]...)
			if typed {
				newControl.ParamTypes[label] = carve(&typeArena, n)
			}
		}
		if typed {
			if len(control.ParamTypes) != 0 {
				newControl.ParamTypes[label] = append(newControl.ParamTypes[label], control.ParamTypes[label]...)
			} else {
				for _, bits := range control.ParamBits[label] {
					newControl.ParamTypes[label] = append(newControl.ParamTypes[label], ir.Scalar(bits))
				}
			}
		}
		for _, id := range blocks[labelBlock[label]].live {
			t := p.Instructions[id].ValueType()
			bits := t.LaneBits
			if t.Kind == ir.MaskKind {
				bits = 64
			}
			newControl.Params[label]++
			newControl.ParamBits[label] = append(newControl.ParamBits[label], bits)
			if typed {
				newControl.ParamTypes[label] = append(newControl.ParamTypes[label], t)
			}
		}
	}
	// Instructions.
	for b := range blocks {
		block := &blocks[b]
		block.firstInsert = len(out.Instructions)
		if block.reachable {
			for k, id := range block.live {
				t := p.Instructions[id].ValueType()
				param := ir.Instruction{Op: ir.OpLabelParam, Imm: uint64(labelParamBase[b] + k), Bits: t.LaneBits}
				if t.Kind != ir.ScalarKind {
					param.Type = t
				}
				if t.Kind == ir.MaskKind {
					param.Bits = 64
				}
				out.Instructions = append(out.Instructions, param)
			}
		} else {
			for _, id := range block.zeros {
				t := p.Instructions[id].ValueType()
				block.zeroAt = append(block.zeroAt, len(out.Instructions))
				switch t.Kind {
				case ir.ScalarKind:
					out.Instructions = append(out.Instructions, ir.Instruction{Op: ir.OpConst, Bits: t.LaneBits})
				case ir.VectorKind:
					out.Instructions = append(out.Instructions, ir.Instruction{Op: ir.OpVZero, Type: t, Bits: t.LaneBits})
				case ir.MaskKind:
					vector := maskVector[t.Lanes]
					zero := len(out.Instructions)
					out.Instructions = append(out.Instructions,
						ir.Instruction{Op: ir.OpVZero, Type: vector, Bits: vector.LaneBits},
						ir.Instruction{Op: ir.OpVCompare, A: zero, B: zero, Imm: uint64(ir.BranchNotEqual), Type: t, Bits: vector.LaneBits})
					block.zeroAt[len(block.zeroAt)-1] = zero + 1
				}
			}
		}
		for index := block.start; index < block.end; index++ {
			ins := p.Instructions[index]
			if ins.Args != nil || ins.Data != nil || ins.Effect != nil {
				ins = ins.Clone()
			}
			switch ins.Op {
			case ir.OpScopeEnter, ir.OpScopeLeave, ir.OpSyscall, ir.OpCallC, ir.OpCallGo:
				for i, id := range ins.Args {
					ins.Args[i] = local(b, id)
				}
			default:
				switch len(ins.InputsInto(buffer[:0])) {
				case 3:
					ins.C = local(b, ins.C)
					fallthrough
				case 2:
					ins.B = local(b, ins.B)
					fallthrough
				case 1:
					ins.A = local(b, ins.A)
				}
			}
			out.Instructions = append(out.Instructions, ins)
		}
	}
	// Control steps.
	// The steps', exits' and variables' lists share chunks.
	var idArena []int
	idCount := 0
	for _, step := range control.Steps {
		idCount += len(step.ResultParts) + len(step.Values) + len(step.Args) + len(step.OtherArgs)
		if !step.IsLabel && step.Term != ir.TermReturn && step.Term != ir.TermTailCall {
			idCount += len(blocks[labelBlock[step.To]].live)
		}
		if !step.IsLabel && step.Term == ir.TermBranch {
			idCount += len(blocks[labelBlock[step.Other]].live)
		}
	}
	for _, exit := range p.ScopeExits {
		idCount += len(exit.Exports)
	}
	for _, v := range p.Variables {
		idCount += len(v.Initial)
	}
	idArena = make([]int, 0, idCount)
	edgeArgs := func(b int, args []int, label int) []int {
		n := len(args) + len(blocks[labelBlock[label]].live)
		if n == 0 {
			return make([]int, 0)
		}
		mapped := carve(&idArena, n)
		for _, id := range args {
			mapped = append(mapped, local(b, id))
		}
		for _, id := range blocks[labelBlock[label]].live {
			mapped = append(mapped, local(b, id))
		}
		return mapped
	}
	mapIDs := func(b int, ids []int) []int {
		if ids == nil {
			return nil
		}
		if len(ids) == 0 {
			return make([]int, 0)
		}
		mapped := carve(&idArena, len(ids))
		for _, id := range ids {
			mapped = append(mapped, local(b, id))
		}
		return mapped
	}
	stepBlock := make([]int, len(control.Steps))
	for b, block := range blocks {
		stepBlock[block.step] = b
	}
	memo := make(map[*ir.PredicateExpr]*ir.PredicateExpr)
	for index, step := range control.Steps {
		if step.IsLabel {
			newControl.Steps[index] = step
			continue
		}
		b := stepBlock[index]
		block := &blocks[b]
		next := step
		next.Start = block.start + shiftBefore[b]
		next.End = block.end + shiftBefore[b+1]
		switch step.Term {
		case ir.TermJump, ir.TermTailCall:
		case ir.TermSwitch:
			next.Selector = local(b, step.Selector)
		default:
			next.Result = local(b, step.Result)
		}
		next.ResultParts = mapIDs(b, step.ResultParts)
		next.Values = mapIDs(b, step.Values)
		if step.Term == ir.TermTailCall {
			next.Args = mapIDs(b, step.Args)
		} else if step.Term != ir.TermReturn {
			next.Args = edgeArgs(b, step.Args, step.To)
		}
		if step.Term == ir.TermBranch {
			next.OtherArgs = edgeArgs(b, step.OtherArgs, step.Other)
			next.Predicate = remapPredicate(step.Predicate, memo, func(id int) int { return local(b, id) })
		}
		newControl.Steps[index] = next
	}
	out.Control = newControl
	// Scope exits and variables.
	leaveBlock := make([]int, len(p.ScopeExits))
	for b, block := range blocks {
		step := &control.Steps[block.step]
		for i := step.LeaveStart; i < step.LeaveEnd; i++ {
			leaveBlock[i] = b
		}
	}
	for i, exit := range p.ScopeExits {
		next := ir.ScopeExit{ID: exit.ID, Parent: exit.Parent, Enter: newIndex(exit.Enter), Position: newIndex(exit.Position-1) + 1}
		for _, id := range exit.Hidden {
			next.Hidden = append(next.Hidden, newIndex(id))
		}
		next.Exports = mapIDs(leaveBlock[i], exit.Exports)
		out.ScopeExits[i] = next
	}
	for i, v := range p.Variables {
		b := blockOf(v.Declaration)
		v.Declaration = newIndex(v.Declaration)
		v.Initial = mapIDs(b, v.Initial)
		out.Variables[i] = v
	}
	return out, nil
}

// sortSearchBlocks returns the block whose segment contains instruction id.
func sortSearchBlocks(blocks []crossBlockBlock, id int) int {
	lo, hi := 0, len(blocks)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if blocks[mid].end > id {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// remapPredicate copies a predicate tree, preserving shared nodes, with each
// operand replaced by remap(operand).
func remapPredicate(root *ir.PredicateExpr, memo map[*ir.PredicateExpr]*ir.PredicateExpr, remap func(int) int) *ir.PredicateExpr {
	clear(memo)
	if root == nil {
		return nil
	}
	// Most branches have one comparison. Its detached copy needs neither a
	// work list nor a memo entry; keep the graph walk for compound predicates.
	if root.Kind == ir.PredicateCompare && root.Left == nil && root.Right == nil {
		out := *root
		out.Value = remap(out.Value)
		if out.WithValue {
			out.Other = remap(out.Other)
		}
		if out.HasAnchor {
			out.Anchor = remap(out.Anchor)
		}
		return &out
	}
	var work []*ir.PredicateExpr
	copyRef := func(n *ir.PredicateExpr) *ir.PredicateExpr {
		if n == nil {
			return nil
		}
		if c := memo[n]; c != nil {
			return c
		}
		c := *n
		memo[n] = &c
		work = append(work, n)
		return &c
	}
	out := copyRef(root)
	for len(work) != 0 {
		n := work[len(work)-1]
		work = work[:len(work)-1]
		c := memo[n]
		c.Left, c.Right = copyRef(n.Left), copyRef(n.Right)
		if c.Kind == ir.PredicateCompare {
			c.Value = remap(c.Value)
			if c.WithValue {
				c.Other = remap(c.Other)
			}
			if c.HasAnchor {
				c.Anchor = remap(c.Anchor)
			}
		}
	}
	return out
}
