package compile

import (
	"errors"
	"fmt"
	"slices"

	"github.com/moriyoshi/nyanjit/ir"
)

// baselineVectors scalarizes the vectors, and vector and mask Vars, of a
// baseline Stream lane by lane before the engine records them. It runs the
// legalizer lowerBaseline runs over a whole program, one segment at a time:
// a vector becomes one value per lane, a mask one 8-bit value per lane, and a
// vector or mask Var one scalar Var per lane.
//
// The engine records a function unchanged until it meets the first vector or
// mask Var or value, which costs scalar code nothing. It then scalarizes the
// rest of that segment and every later one. What it recorded keeps its IDs:
// those values and Vars stand for themselves. Vector label parameters are
// not supported.
type baselineVectors struct {
	l vectorLegalizer
	// source keeps the type of each source value; out is the scalar program
	// the engine has recorded, whose IDs are the engine's.
	source, out ir.Program
	vars        int // source Vars seen
	seg         ir.Segment
	exits       []ir.ScopeExit
	ints        []int // the current segment's lane lists
}

// errBaselineVector is how the engine reports its first vector or mask Var
// or value.
var errBaselineVector = errors.New("baseline tier: vector or mask")

// scalarSegment records seg scalarized after the engine met a vector or
// mask Var, the one at declared, in it. It first undoes the segment's labels
// and its Vars before that one.
func (e *baselineEngine) scalarSegment(seg *ir.Segment, declared int) error {
	if e.vectors != nil || e.wholeProgram {
		return baselineUnsupported("vector or mask Var")
	}
	for _, l := range seg.Labels {
		e.labelBlock[l] = -1
	}
	if declared > 0 {
		k := len(e.vars) - declared
		e.partVar = e.partVar[:e.vars[k].part]
		e.vars = e.vars[:k]
	}
	e.startVectors()
	return e.segment(seg)
}

// scalarRest returns the rest of seg, from instruction k, scalarized, after
// the engine met a vector there; block b records it.
func (e *baselineEngine) scalarRest(seg *ir.Segment, k int, b int32) (*ir.Segment, error) {
	if e.vectors != nil || e.wholeProgram {
		return nil, baselineUnsupported("vector operation %v", seg.Code[k].Op)
	}
	e.startVectors()
	rest, err := e.vectors.segment(e, seg, k)
	if err != nil {
		return nil, err
	}
	e.blocks[b].end = uint32(rest.Step.End)
	return rest, nil
}

// startVectors starts the scalarizer after the records so far, which stand
// for themselves.
func (e *baselineEngine) startVectors() {
	options := e.options
	options.Features = []string{}
	arch := e.target.architecture
	bits := uint8(64)
	if arch == "386" {
		bits = 32
	}
	n := len(e.recs)
	v := &baselineVectors{}
	v.source.Instructions = make([]ir.Instruction, n, 2*n+64)
	v.out.Instructions = make([]ir.Instruction, n, 4*n+256)
	v.l = vectorLegalizer{source: &v.source, out: &v.out, values: make([]legalizedValue, n, 2*n+64), boundary: make([]int, n+1, 2*n+65),
		arch: arch, bits: bits, info: vectorInfo(options)}
	for id := range e.recs {
		// Only the type of an earlier value is read: as an address, or
		// as a scalar operand of a vector operation.
		t := ir.Instruction{Bits: e.recs[id].bits}
		v.source.Instructions[id], v.out.Instructions[id] = t, t
		v.l.values[id] = legalizedValue{lanes: v.l.lane(id)}
		v.l.boundary[id] = id
	}
	v.l.boundary[n] = n
	v.out.Variables = make([]ir.Variable, len(e.vars))
	v.l.variableIDs = make([][]int, len(e.vars))
	for k := range e.vars {
		v.l.variableIDs[k] = v.l.lane(k)
	}
	v.vars = len(e.vars)
	e.vectors = v
}

// laneCount returns the number of scalars a value of type t becomes.
func laneCount(t ir.Type) int {
	if t.Kind == ir.VectorKind || t.Kind == ir.MaskKind {
		return int(t.Lanes)
	}
	return 1
}

// segment converts seg to its scalar form from instruction from on. With
// from > 0 the engine has recorded the instructions before it and the
// segment's Vars, which are scalars.
func (v *baselineVectors) segment(e *baselineEngine, seg *ir.Segment, from int) (*ir.Segment, error) {
	l := &v.l
	base, end := seg.Base, seg.Base+len(seg.Code)
	if base+from != len(v.source.Instructions) {
		return nil, baselineInvalid(-1, 0, "segment base %d does not continue at %d", base, len(v.source.Instructions))
	}
	v.ints = v.ints[:0]
	outBase, firstVar := len(v.out.Instructions), len(v.out.Variables)
	// Each Var gets its lane Vars now; their declarations and initial
	// values follow the code.
	if from == 0 {
		for i := range seg.Variables {
			x := &seg.Variables[i]
			if !x.Type.Valid() {
				return nil, baselineInvalid(-1, 0, "Var %d has an invalid type", v.vars+i)
			}
			count := laneCount(x.Type)
			ids := l.lanesFor(count)
			for range count {
				lane := *x
				lane.Initial = nil
				if x.Type.Kind == ir.VectorKind {
					lane.Type = ir.Scalar(x.Type.LaneBits)
				} else if x.Type.Kind == ir.MaskKind {
					lane.Type = ir.Scalar(8)
				}
				ids = append(ids, len(v.out.Variables))
				v.out.Variables = append(v.out.Variables, lane)
			}
			l.variableIDs = append(l.variableIDs, ids)
		}
	}
	l.values = slices.Grow(l.values, len(seg.Code))[:end]
	clear(l.values[base+from:])
	l.boundary = slices.Grow(l.boundary, len(seg.Code))[:end+1]
	for i := from; i < len(seg.Code); i++ {
		ins := &seg.Code[i]
		v.source.Instructions = append(v.source.Instructions, ir.Instruction{Op: ins.Op, Type: ins.Type, Bits: ins.Bits})
	}
	budget := legalizedBudget(end)
	for i := from; i < len(seg.Code); i++ {
		id, ins := base+i, &seg.Code[i]
		l.boundary[id] = len(v.out.Instructions)
		if err := v.check(id, ins); err != nil {
			return nil, err
		}
		if err := l.operation(id, *ins); err != nil {
			if errors.Is(err, ErrInvalidFunction) {
				return nil, baselineInvalid(id, ins.Op, "vector operation has invalid operands")
			}
			return nil, fmt.Errorf("SIMD value %d: %w", id, err)
		}
		if ins.Op.ProducesValue() && len(l.values[id].lanes) != laneCount(ins.ValueType()) {
			return nil, baselineInvalid(id, ins.Op, "value does not have type %+v", ins.ValueType())
		}
		if len(v.out.Instructions) > budget {
			return nil, fmt.Errorf("SIMD value %d: scalar lowering exceeds %d instructions for a %d-instruction program: %w", id, budget, end, ErrBackendLimit)
		}
	}
	l.boundary[end] = len(v.out.Instructions)
	if from > 0 {
		if err := v.redeclare(e, seg.Variables, end); err != nil {
			return nil, err
		}
	} else {
		for i := range seg.Variables {
			if err := v.declare(v.vars+i, &seg.Variables[i], end); err != nil {
				return nil, err
			}
		}
		v.vars += len(seg.Variables)
	}
	exits := v.exits[:0]
	for i := range seg.ScopeExits {
		x := seg.ScopeExits[i]
		if x.Enter < 0 || x.Enter > end || x.Position < 0 || x.Position > end {
			return nil, baselineInvalid(-1, 0, "scope exit %d is out of range", x.ID)
		}
		x.Enter, x.Position = l.boundary[x.Enter], l.boundary[x.Position]
		var err error
		if x.Exports, err = v.expand(x.Exports, end, "scope export"); err != nil {
			return nil, err
		}
		if x.Hidden, err = v.expand(x.Hidden, end, "hidden scope value"); err != nil {
			return nil, err
		}
		exits = append(exits, x)
	}
	v.exits = exits
	step := seg.Step
	step.Start, step.End = outBase, len(v.out.Instructions)
	var err error
	switch step.Term {
	case ir.TermReturn:
		var ok bool
		if step.Result, ok = v.scalar(step.Result, end); !ok {
			return nil, baselineInvalid(-1, 0, "segment result %d is not a preceding scalar", seg.Step.Result)
		}
		if step.ResultParts, err = v.scalars(step.ResultParts, end, "result part", false); err != nil {
			return nil, err
		}
	case ir.TermTailCall:
		if step.Args, err = v.scalars(step.Args, end, "tail call operand", false); err != nil {
			return nil, err
		}
	case ir.TermJump, ir.TermBranch, ir.TermSwitch:
		if step.Term == ir.TermSwitch {
			var ok bool
			if step.Selector, ok = v.scalar(step.Selector, end); !ok {
				return nil, baselineInvalid(-1, 0, "switch selector %d is not a preceding scalar", seg.Step.Selector)
			}
		}
		if step.Args, err = v.scalars(step.Args, end, "edge argument", true); err != nil {
			return nil, err
		}
		if step.OtherArgs, err = v.scalars(step.OtherArgs, end, "edge argument", true); err != nil {
			return nil, err
		}
		if step.Values, err = v.scalars(step.Values, end, "branch value", false); err != nil {
			return nil, err
		}
		if step.Predicate, err = v.predicate(step.Predicate, end); err != nil {
			return nil, err
		}
	}
	v.seg = ir.Segment{Labels: seg.Labels, Base: outBase, Code: v.out.Instructions[outBase:], Variables: v.out.Variables[firstVar:],
		ScopeExits: exits, Step: step, ParameterTypes: seg.ParameterTypes, Finalizers: seg.Finalizers}
	return &v.seg, nil
}

// check validates what the legalizer assumes of instruction id, which
// Program.Validate checks on the whole-program path.
func (v *baselineVectors) check(id int, ins *ir.Instruction) error {
	l := &v.l
	var buffer [4]int
	inputs := ins.InputsInto(buffer[:0])
	for _, x := range inputs {
		if x < 0 || x >= id {
			return baselineInvalid(id, ins.Op, "uses value %d, which does not precede it", x)
		}
	}
	switch ins.Op {
	case ir.OpGet, ir.OpSet:
		if ins.Variable < 0 || ins.Variable >= len(l.variableIDs) {
			return baselineInvalid(id, ins.Op, "Var %d out of range", ins.Variable)
		}
		if ins.Op == ir.OpSet {
			if n := len(l.variableIDs[ins.Variable]); n > 1 && len(l.values[ins.A].lanes) != n || n == 1 && len(l.values[ins.A].lanes) != 1 {
				return baselineInvalid(id, ins.Op, "value %d does not have the type of Var %d", ins.A, ins.Variable)
			}
		}
		return nil
	case ir.OpScopeEnter, ir.OpScopeLeave:
		return nil
	case ir.OpLabelParam:
		if ins.IsVector() {
			return baselineUnsupported("vector or mask label parameter")
		}
	}
	if !ins.IsVector() {
		for _, x := range inputs {
			if len(l.values[x].lanes) != 1 {
				return baselineInvalid(id, ins.Op, "scalar operation cannot consume vector or mask value %d", x)
			}
		}
		return nil
	}
	for _, x := range inputs {
		if len(l.values[x].lanes) != laneCount(v.source.Instructions[x].ValueType()) {
			return baselineInvalid(id, ins.Op, "uses non-value %d", x)
		}
	}
	if !v.vectorShape(ins) {
		return baselineInvalid(id, ins.Op, "vector operation has invalid shape or operands")
	}
	return nil
}

// vectorShape checks a vector operation's type and operand types as
// Program.Validate does.
func (v *baselineVectors) vectorShape(ins *ir.Instruction) bool {
	t := ins.ValueType()
	if !t.Valid() || !ins.ByteOrder.Valid() || ins.ByteOrder != ir.ByteOrderNative && ins.Op != ir.OpVLoad && ins.Op != ir.OpVStore {
		return false
	}
	if ins.Op != ir.OpVCompare && ins.Bits != t.LaneBits {
		return false
	}
	input := func(x int) ir.Type { return v.source.Instructions[x].ValueType() }
	address := func(x int) bool {
		return input(x).Kind == ir.ScalarKind && (input(x).LaneBits == 32 || input(x).LaneBits == 64)
	}
	vector := t.Kind == ir.VectorKind
	switch ins.Op {
	case ir.OpVLoad:
		return vector && address(ins.A)
	case ir.OpVStore:
		return vector && input(ins.B) == t && address(ins.A)
	case ir.OpVSplat:
		return vector && input(ins.A) == ir.Scalar(t.LaneBits)
	case ir.OpVConst:
		return vector && len(ins.Data) == int(t.Bits()/8)
	case ir.OpVZero:
		return vector
	case ir.OpVExtract:
		a := input(ins.A)
		return a.Kind == ir.VectorKind && t == ir.Scalar(a.LaneBits) && ins.Imm < uint64(a.Lanes)
	case ir.OpVInsert, ir.OpVInsertZero:
		return vector && input(ins.A) == t && input(ins.B) == ir.Scalar(t.LaneBits) && ins.Imm < uint64(t.Lanes)
	case ir.OpVCompare:
		a := input(ins.A)
		return a.Kind == ir.VectorKind && input(ins.B) == a && t == ir.Mask(a.Lanes) && ins.Bits == a.LaneBits && ins.Imm <= uint64(ir.BranchAbove)
	case ir.OpVMaskBits:
		a := input(ins.A)
		return a.Kind == ir.MaskKind && t.Kind == ir.ScalarKind && t.LaneBits <= 64 && uint16(t.LaneBits) >= a.Lanes
	case ir.OpVCarrylessMul:
		return vector && t.LaneBits == 64 && t.Lanes%2 == 0 && input(ins.A) == t && input(ins.B) == t && ins.Imm <= 3
	case ir.OpVShuffle:
		if !vector || input(ins.A) != t || input(ins.B) != t || len(ins.Data) != int(t.Lanes) {
			return false
		}
		for _, lane := range ins.Data {
			if uint16(lane) >= 2*t.Lanes {
				return false
			}
		}
		return true
	case ir.OpVExtendUnsigned, ir.OpVExtendSigned:
		a := input(ins.A)
		return vector && a.Kind == ir.VectorKind && 2*a.LaneBits == t.LaneBits && (a.Lanes == t.Lanes && ins.Imm == 0 || a.Lanes == 2*t.Lanes && ins.Imm <= 1)
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
		return vector && input(ins.A) == t && input(ins.B) == t
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
		return vector && input(ins.A) == t
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		b := input(ins.B)
		return vector && input(ins.A) == t && (b == t || b.Kind == ir.ScalarKind && b.LaneBits <= 64)
	}
	return false
}

// declare completes the lane Vars of source Var k, declared in the segment
// that ends at end.
func (v *baselineVectors) declare(k int, x *ir.Variable, end int) error {
	l := &v.l
	if x.Declaration < 0 || x.Declaration >= end {
		return baselineInvalid(-1, 0, "Var %d is not declared in its segment", k)
	}
	ids := l.variableIDs[k]
	for lane, dst := range ids {
		out := &v.out.Variables[dst]
		out.Declaration = l.boundary[x.Declaration]
		out.Initial = carve(&v.ints, len(x.Initial))
		for _, initial := range x.Initial {
			if initial < 0 || initial >= x.Declaration {
				return baselineInvalid(-1, 0, "Var %d: initial value %d does not precede its declaration", k, initial)
			}
			lanes := l.values[initial].lanes
			switch {
			case len(ids) > 1 && len(lanes) == len(ids):
				out.Initial = append(out.Initial, lanes[lane])
			case len(ids) == 1 && len(lanes) == 1:
				out.Initial = append(out.Initial, lanes[0])
			default:
				return baselineInvalid(-1, 0, "Var %d: initial value %d does not have its type", k, initial)
			}
		}
		if !x.Immutable && len(ids) > 1 {
			out.Declaration += lane
		}
	}
	return nil
}

// redeclare moves the declarations and initial values of the scalar Vars
// the engine recorded for a segment it then scalarized to their scalar IDs.
func (v *baselineVectors) redeclare(e *baselineEngine, vars []ir.Variable, end int) error {
	first := len(e.vars) - len(vars)
	for i := range vars {
		x, bv := &vars[i], &e.vars[first+i]
		if x.Declaration < 0 || x.Declaration >= end {
			return baselineInvalid(-1, 0, "Var %d is not declared in its segment", first+i)
		}
		bv.decl = int32(v.l.boundary[x.Declaration])
		for p, initial := range x.Initial {
			s, ok := v.scalar(initial, end)
			if !ok {
				return baselineInvalid(-1, 0, "Var %d: initial value %d is not a preceding scalar", first+i, initial)
			}
			bv.initial[p] = uint32(s)
		}
	}
	return nil
}

// scalar returns the scalar standing for source value x before end.
func (v *baselineVectors) scalar(x, end int) (int, bool) {
	if x < 0 || x >= end || len(v.l.values[x].lanes) != 1 {
		return 0, false
	}
	return v.l.values[x].lanes[0], true
}

// scalars maps values that must be scalars. A vector edge argument is a gap:
// vector label parameters are not supported.
func (v *baselineVectors) scalars(ids []int, end int, what string, edge bool) ([]int, error) {
	if len(ids) == 0 {
		return ids, nil
	}
	out := carve(&v.ints, len(ids))
	for _, x := range ids {
		s, ok := v.scalar(x, end)
		if !ok {
			if edge && x >= 0 && x < end && len(v.l.values[x].lanes) > 1 {
				return nil, baselineUnsupported("vector or mask label argument")
			}
			return nil, baselineInvalid(-1, 0, "%s %d is not a preceding scalar", what, x)
		}
		out = append(out, s)
	}
	return out, nil
}

// expand maps values to all their lanes.
func (v *baselineVectors) expand(ids []int, end int, what string) ([]int, error) {
	if len(ids) == 0 {
		return ids, nil
	}
	out := carve(&v.ints, len(ids))
	for _, x := range ids {
		if x < 0 || x >= end {
			return nil, baselineInvalid(-1, 0, "%s %d out of range", what, x)
		}
		out = append(out, v.l.values[x].lanes...)
	}
	return out, nil
}

// predicate copies a branch predicate with its compared values mapped.
func (v *baselineVectors) predicate(p *ir.PredicateExpr, end int) (*ir.PredicateExpr, error) {
	if p == nil {
		return nil, nil // the engine reports it
	}
	p = ir.RebasePredicate(p, 0)
	for node, err := range predicateComparisons(p) {
		if err != nil {
			return nil, baselineInvalid(-1, 0, "predicate is malformed")
		}
		var ok bool
		if node.Value, ok = v.scalar(node.Value, end); !ok {
			return nil, baselineInvalid(-1, 0, "predicate compares a value that is not a preceding scalar")
		}
		if node.WithValue {
			if node.Other, ok = v.scalar(node.Other, end); !ok {
				return nil, baselineInvalid(-1, 0, "predicate compares a value that is not a preceding scalar")
			}
		}
		if node.HasAnchor {
			if anchor, ok := v.scalar(node.Anchor, end); ok {
				node.Anchor = anchor
			}
		}
	}
	return p, nil
}
