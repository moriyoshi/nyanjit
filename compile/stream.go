package compile

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// Stream lowers one function for a Target as its front end produces it, one
// ir.Segment at a time. A Builder from nyanjit.NewBuilderFor drives a Stream;
// raw-IR front ends may drive one directly.
//
// For a TierBaseline Target, Segment converts each segment to the baseline's
// compact form and keeps no ir.Instruction. For a TierOptimized Target,
// Segment records the segment and Unit lowers the whole program as Lower
// does; the same segments then give output identical to Lower's for the
// concatenated program.
//
// Segment and Unit check what they receive. Rule violations return an error
// wrapping ErrInvalidFunction, located by a *ir.ValidationError with a
// program-wide ID. Limits return ErrBackendLimit, or a *LimitError when the
// limit is a number. Gaps in the baseline tier return ErrUnsupported. After
// the first error, every later call returns it. A Stream is not safe for
// concurrent use.
type Stream struct {
	target   Target
	f        Func
	err      error
	done     bool
	recorder *segmentRecorder
	baseline *baselineEngine
}

// NewStream starts lowering a function entered as f describes, for t. It
// returns Lower's errors for an invalid Target, Context or ABI, and
// ErrUnsupported when t is TierBaseline and f asks for what that tier does not
// implement yet (§6.2.6).
func NewStream(t Target, f Func) (*Stream, error) {
	if t == nil || t.resolved() == nil {
		return nil, ErrInvalidFunction
	}
	if f.Context > ContextExternal {
		return nil, fmt.Errorf("unknown call context %d: %w: %w", f.Context, abi.ErrInvalidSpec, ErrInvalidFunction)
	}
	s := &Stream{target: t, f: f}
	if t.resolved().spec.Tier == TierBaseline {
		engine, err := newBaselineEngine(t.resolved(), f)
		if err != nil {
			return nil, err
		}
		s.baseline = engine
		return s, nil
	}
	s.recorder = getSegmentRecorder()
	return s, nil
}

// Segment consumes the function's next segment in layout order. It does not
// retain seg or anything it refers to after it returns, except the
// Finalizers, which the Unit's Codes share.
func (s *Stream) Segment(seg *ir.Segment) error {
	if s == nil {
		return ErrInvalidFunction
	}
	if s.err != nil {
		return s.err
	}
	if s.done {
		return fmt.Errorf("Segment after Unit: %w", ErrInvalidFunction)
	}
	if seg == nil {
		s.err = fmt.Errorf("nil segment: %w", ErrInvalidFunction)
		return s.err
	}
	var err error
	if s.baseline != nil {
		err = s.baseline.segment(seg)
	} else {
		err = s.recorder.segment(seg)
	}
	if err != nil {
		s.err = err
		if s.baseline != nil {
			s.baseline.recycle()
			s.baseline = nil
		}
	}
	return err
}

// Unit ends the function and returns its Unit. It checks rules that span
// segments (dominance, unbound labels, a complete function) and returns an
// error wrapping ErrInvalidFunction for a violation. Unit consumes the
// Stream; a second call returns ErrInvalidFunction.
func (s *Stream) Unit() (*Unit, error) {
	if s == nil {
		return nil, ErrInvalidFunction
	}
	if s.done {
		return nil, fmt.Errorf("Unit called twice: %w", ErrInvalidFunction)
	}
	s.done = true
	if s.err != nil {
		return nil, s.err
	}
	if s.baseline != nil {
		u, err := s.baseline.unit()
		if err == nil && s.baseline.vectors != nil {
			u.baseline.vectorOperations = s.baseline.vectors.l.reports
		}
		s.baseline.recycle()
		s.baseline = nil
		s.err = err
		return u, err
	}
	program, err := s.recorder.program()
	putSegmentRecorder(s.recorder)
	s.recorder = nil
	if err != nil {
		s.err = err
		return nil, err
	}
	sealed, err := ir.SealOwned(program)
	if err != nil {
		s.err = invalidSource(err)
		return nil, s.err
	}
	u, err := Lower(s.target, sealed, s.f)
	s.err = err
	return u, err
}

// segmentRecorder concatenates segments into an ir.Program for the
// optimized tier. Label tables, which segments do not carry, are inferred:
// a label's scope is the scope active where it is bound, and its parameters
// come from the edges that target it and the OpLabelParam reads of its block.
//
// Its tables are pooled scratch: program copies them to exact-size storage
// the program owns, and putSegmentRecorder clears them for the next Stream.
type segmentRecorder struct {
	instructions []ir.Instruction
	variables    []ir.Variable
	exits        []ir.ScopeExit
	steps        []ir.ControlStep
	paramTypes   []reflect.Type
	finalizers   backend.Finalizers
	// ints holds the program's small owned lists (Args, Initial, Hidden,
	// step operands). It is never pooled: the program keeps its chunks.
	ints        []int
	segments    int
	firstLabels bool
	// bindings[l] is the segment index binding label l, plus one.
	bindings []int
	// segmentStart[i] is the base of segment i; labelScope[l] its scope.
	segmentStart []int
	labelScope   []int
	scopes       []int // the active scope stack while recording
	varSegment   []int // the segment that declared each Variable
	stackAreas   []ir.StackArea
	// program's scratch: each label's parameter types, and which are known
	types [][]ir.Type
	known [][]bool
}

var segmentRecorders = sync.Pool{New: func() any { return new(segmentRecorder) }}

func getSegmentRecorder() *segmentRecorder { return segmentRecorders.Get().(*segmentRecorder) }

// putSegmentRecorder clears r's references into the finished program and
// pools its tables, unless one has grown past scratchBufferLimit.
func putSegmentRecorder(r *segmentRecorder) {
	if bufferBytes(r.instructions) > scratchBufferLimit || bufferBytes(r.steps) > scratchBufferLimit ||
		bufferBytes(r.variables) > scratchBufferLimit || bufferBytes(r.exits) > scratchBufferLimit {
		return
	}
	clear(r.instructions)
	clear(r.variables)
	clear(r.exits)
	clear(r.steps)
	for i := range r.known {
		r.known[i] = r.known[i][:0]
	}
	for i := range r.types {
		r.types[i] = r.types[i][:0]
	}
	*r = segmentRecorder{
		instructions: r.instructions[:0], variables: r.variables[:0], exits: r.exits[:0], steps: r.steps[:0],
		bindings: r.bindings[:0], segmentStart: r.segmentStart[:0], labelScope: r.labelScope[:0],
		scopes: r.scopes[:0], varSegment: r.varSegment[:0], stackAreas: r.stackAreas[:0], types: r.types, known: r.known,
	}
	segmentRecorders.Put(r)
}

// ownedInts copies src into r.ints, keeping nil for an empty source.
func (r *segmentRecorder) ownedInts(src []int) []int { return carveCopy(&r.ints, src) }

// owned returns an exact-size copy of a recorder table, or nil when it is
// empty, as appending to a nil table gave.
func owned[T any](src []T) []T {
	if len(src) == 0 {
		return nil
	}
	return append(make([]T, 0, len(src)), src...)
}

func (r *segmentRecorder) segment(seg *ir.Segment) error {
	if seg.Base != len(r.instructions) {
		return segmentError(seg.Base, "segment base %d does not continue at %d", seg.Base, len(r.instructions))
	}
	if seg.Step.IsLabel {
		return segmentError(-1, "segment step is a label step")
	}
	if seg.Step.Start != seg.Base || seg.Step.End != seg.Base+len(seg.Code) || seg.Step.LeaveStart != len(r.exits) || seg.Step.LeaveEnd != len(r.exits)+len(seg.ScopeExits) {
		return segmentError(-1, "segment %d step coordinates disagree with its code and scope exits", r.segments)
	}
	if r.segments == 0 {
		r.paramTypes = append([]reflect.Type(nil), seg.ParameterTypes...)
		r.firstLabels = len(seg.Labels) != 0
		r.scopes = append(r.scopes[:0], 0)
	} else if len(seg.Labels) == 0 {
		return segmentError(seg.Base, "segment %d binds no label", r.segments)
	}
	scope := r.scopes[len(r.scopes)-1]
	for _, l := range seg.Labels {
		if l < 0 {
			return segmentError(-1, "negative label %d", l)
		}
		for int(l) >= len(r.bindings) {
			r.bindings = append(r.bindings, 0)
			r.labelScope = append(r.labelScope, 0)
		}
		if r.bindings[l] != 0 {
			return segmentError(-1, "label %d bound twice", l)
		}
		r.bindings[l] = r.segments + 1
		r.labelScope[l] = scope
		r.steps = append(r.steps, ir.ControlStep{IsLabel: true, Label: int(l)})
	}
	r.segmentStart = append(r.segmentStart, seg.Base)
	start := len(r.instructions)
	r.instructions = append(r.instructions, seg.Code...)
	for i := range seg.Code {
		ins := &r.instructions[start+i]
		// Clone's copies: Args share the recorder's int chunks.
		ins.Args = r.ownedInts(ins.Args)
		if ins.Data != nil {
			ins.Data = append([]byte(nil), ins.Data...)
		}
		if ins.Effect != nil {
			effect := ins.Effect.Clone()
			ins.Effect = &effect
		}
		switch ins.Op {
		case ir.OpScopeEnter:
			r.scopes = append(r.scopes, int(ins.Imm))
		case ir.OpScopeLeave:
			if len(r.scopes) > 1 {
				r.scopes = r.scopes[:len(r.scopes)-1]
			}
		}
	}
	r.stackAreas = append(r.stackAreas, seg.StackAreas...)
	r.finalizers = appendFinalizers(r.finalizers, seg.Finalizers)
	for _, v := range seg.Variables {
		v.Initial = r.ownedInts(v.Initial)
		r.variables = append(r.variables, v)
		r.varSegment = append(r.varSegment, r.segments)
	}
	for _, x := range seg.ScopeExits {
		x.Hidden = r.ownedInts(x.Hidden)
		x.Exports = r.ownedInts(x.Exports)
		r.exits = append(r.exits, x)
	}
	step := seg.Step
	step.Values = r.ownedInts(step.Values)
	step.ResultParts = r.ownedInts(step.ResultParts)
	step.Args = r.ownedInts(step.Args)
	step.OtherArgs = r.ownedInts(step.OtherArgs)
	step.Cases = r.ownedInts(step.Cases)
	if step.Predicate != nil {
		step.Predicate = ir.RebasePredicate(step.Predicate, 0)
	}
	r.steps = append(r.steps, step)
	r.segments++
	return nil
}

func (r *segmentRecorder) program() (*ir.Program, error) {
	if r.segments == 0 {
		return nil, fmt.Errorf("function has no segments: %w", ErrInvalidFunction)
	}
	arity := 0
	for i := range r.instructions {
		if r.instructions[i].Op == ir.OpParam {
			arity++
		}
	}
	p := &ir.Program{
		Instructions: owned(r.instructions), Variables: owned(r.variables), ScopeExits: owned(r.exits),
		Arity: arity, Result: -1, StackAreas: owned(r.stackAreas),
	}
	if len(r.paramTypes) != 0 {
		p.ParameterTypes = r.paramTypes
	}
	p.Finalizers = r.finalizers
	// Captures declared after their scope's entry segment extend its Args.
	for v := range p.Variables {
		variable := &p.Variables[v]
		if !variable.Immutable || variable.Declaration < 0 || variable.Declaration >= len(p.Instructions) {
			continue
		}
		enter := &p.Instructions[variable.Declaration]
		if enter.Op != ir.OpScopeEnter || r.varSegment[v] <= r.segmentOf(variable.Declaration) {
			continue
		}
		enter.Args = append(enter.Args, variable.Initial...)
	}
	if r.segments == 1 && !r.firstLabels && r.steps[len(r.steps)-1].Term != ir.TermTailCall {
		step := r.steps[len(r.steps)-1]
		if step.Term != ir.TermReturn {
			return nil, segmentError(-1, "a function of one segment must return")
		}
		p.Result, p.ResultParts = step.Result, step.ResultParts
		return p, nil
	}
	n := len(r.bindings)
	var steps []ir.ControlStep
	if !r.firstLabels {
		entry := n
		n++
		steps = make([]ir.ControlStep, 1, 1+len(r.steps))
		steps[0] = ir.ControlStep{IsLabel: true, Label: entry}
		steps = append(steps, r.steps...)
		r.bindings = append(r.bindings, 1)
		r.labelScope = append(r.labelScope, 0)
	} else {
		steps = owned(r.steps)
	}
	for l := 0; l < n; l++ {
		if r.bindings[l] == 0 {
			return nil, fmt.Errorf("label %d is unbound: %w", l, ErrInvalidFunction)
		}
	}
	tables := make([]int, 2*n)
	control := &ir.Control{
		Labels: make([]bool, n), LabelScopes: append(tables[:0:n], r.labelScope[:n]...),
		Params: tables[n:], ParamBits: make([][]uint8, n), ParamTypes: make([][]ir.Type, n),
		Steps: steps,
	}
	for l := range control.Labels {
		control.Labels[l] = true
	}
	// Parameter counts and types: OpLabelParam reads first, edge arguments
	// for parameters the block never reads.
	for len(r.known) < n {
		r.known = append(r.known, nil)
	}
	known := r.known[:n]
	for len(r.types) < n {
		r.types = append(r.types, nil)
	}
	types := r.types[:n]
	setType := func(l, i int, t ir.Type, force bool) {
		for len(types[l]) <= i {
			types[l] = append(types[l], ir.Type{})
			known[l] = append(known[l], false)
		}
		if force || !known[l][i] {
			types[l][i], known[l][i] = t, true
		}
	}
	var blockLabels []int
	for i := range steps {
		step := &steps[i]
		if step.IsLabel {
			blockLabels = append(blockLabels, step.Label)
			continue
		}
		for pos := step.Start; pos < step.End && pos < len(r.instructions); pos++ {
			ins := &r.instructions[pos]
			if ins.Op != ir.OpLabelParam || ins.Imm > 1<<20 {
				continue
			}
			for _, l := range blockLabels {
				setType(l, int(ins.Imm), ins.ValueType(), true)
			}
		}
		blockLabels = blockLabels[:0]
	}
	edge := func(l int, args []int) {
		if l < 0 || l >= n {
			return
		}
		for i, a := range args {
			if a >= 0 && a < len(r.instructions) {
				setType(l, i, r.instructions[a].ValueType(), false)
			}
		}
		if len(args) > len(types[l]) {
			for i := len(types[l]); i < len(args); i++ {
				setType(l, i, ir.Scalar(64), false)
			}
		}
	}
	for i := range steps {
		step := &steps[i]
		if step.IsLabel {
			continue
		}
		switch step.Term {
		case ir.TermJump:
			edge(step.To, step.Args)
		case ir.TermBranch:
			edge(step.To, step.Args)
			edge(step.Other, step.OtherArgs)
		case ir.TermSwitch:
			edge(step.To, step.Args)
			for _, l := range step.Cases {
				edge(l, step.Args)
			}
		}
	}
	total := 0
	for l := range types {
		total += len(types[l])
	}
	typeArena, bitArena := make([]ir.Type, 0, total), make([]uint8, 0, total)
	for l := 0; l < n; l++ {
		control.Params[l] = len(types[l])
		if len(types[l]) == 0 {
			continue
		}
		start := len(typeArena)
		typeArena = append(typeArena, types[l]...)
		control.ParamTypes[l] = typeArena[start:len(typeArena):len(typeArena)]
		for _, t := range types[l] {
			bits := t.LaneBits
			if t.Kind == ir.MaskKind {
				bits = 64
			}
			bitArena = append(bitArena, bits)
		}
		control.ParamBits[l] = bitArena[start:len(bitArena):len(bitArena)]
	}
	p.Control = control
	return p, nil
}

// segmentOf returns the index of the segment holding instruction pos.
func (r *segmentRecorder) segmentOf(pos int) int {
	lo, hi := 0, len(r.segmentStart)
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if r.segmentStart[mid] <= pos {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// segmentError reports a malformed segment stream as a validation error.
func segmentError(index int, format string, args ...any) error {
	return fmt.Errorf("%w: %w", ErrInvalidFunction, &ir.ValidationError{Index: index, Rule: fmt.Sprintf(format, args...)})
}
