package builder

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"github.com/moriyoshi/nyanjit/ir"
)

// SegmentRecorder keeps copies of a segment Builder's segments and
// concatenates them into a Program as the ir.Segment doc describes. It is
// the reference consumer of the equivalence tests.
type SegmentRecorder struct {
	Segments []ir.Segment
	// FailAt makes Emit return ErrFailedEmit for the segment with this
	// index, when it is positive or zero.
	FailAt int
}

// ErrFailedEmit is what Emit returns at FailAt.
var ErrFailedEmit = errors.New("test emit failure")

func NewSegmentRecorder() *SegmentRecorder { return &SegmentRecorder{FailAt: -1} }

// Emit copies s, which the Builder reuses.
func (r *SegmentRecorder) Emit(s *ir.Segment) error {
	if len(r.Segments) == r.FailAt {
		r.Segments = append(r.Segments, ir.Segment{})
		return ErrFailedEmit
	}
	c := ir.Segment{Labels: slices.Clone(s.Labels), Base: s.Base, ParameterTypes: slices.Clone(s.ParameterTypes)}
	c.Code = make([]ir.Instruction, len(s.Code))
	for i := range s.Code {
		c.Code[i] = s.Code[i].Clone()
	}
	c.Variables = make([]ir.Variable, len(s.Variables))
	for i, v := range s.Variables {
		v.Initial = slices.Clone(v.Initial)
		c.Variables[i] = v
	}
	c.ScopeExits = make([]ir.ScopeExit, len(s.ScopeExits))
	for i, e := range s.ScopeExits {
		e.Hidden, e.Exports = slices.Clone(e.Hidden), slices.Clone(e.Exports)
		c.ScopeExits[i] = e
	}
	c.Step = s.Step
	c.Step.Values, c.Step.Args, c.Step.OtherArgs, c.Step.ResultParts = slices.Clone(s.Step.Values), slices.Clone(s.Step.Args), slices.Clone(s.Step.OtherArgs), slices.Clone(s.Step.ResultParts)
	c.Step.Predicate = copyPredicate(s.Step.Predicate, map[*ir.PredicateExpr]*ir.PredicateExpr{})
	r.Segments = append(r.Segments, c)
	return nil
}

func copyPredicate(p *ir.PredicateExpr, memo map[*ir.PredicateExpr]*ir.PredicateExpr) *ir.PredicateExpr {
	if p == nil {
		return nil
	}
	if c := memo[p]; c != nil {
		return c
	}
	c := new(ir.PredicateExpr)
	*c = *p
	memo[p] = c
	c.Left, c.Right = copyPredicate(p.Left, memo), copyPredicate(p.Right, memo)
	return c
}

// Program concatenates the segments. A function with no labels has no
// Control, as Finish gives it.
func (r *SegmentRecorder) Program() (*ir.Program, error) {
	p := &ir.Program{Result: -1}
	labels := 0
	for i, s := range r.Segments {
		if s.Base != len(p.Instructions) {
			return nil, fmt.Errorf("segment %d: Base %d, want %d", i, s.Base, len(p.Instructions))
		}
		if i != 0 && len(s.ParameterTypes) != 0 {
			return nil, fmt.Errorf("segment %d has ParameterTypes", i)
		}
		if s.Step.IsLabel || s.Step.Start != s.Base || s.Step.End != s.Base+len(s.Code) ||
			s.Step.LeaveStart != len(p.ScopeExits) || s.Step.LeaveEnd != len(p.ScopeExits)+len(s.ScopeExits) {
			return nil, fmt.Errorf("segment %d: step %+v does not cover the segment", i, s.Step)
		}
		p.Instructions = append(p.Instructions, s.Code...)
		p.Variables = append(p.Variables, s.Variables...)
		p.ScopeExits = append(p.ScopeExits, s.ScopeExits...)
		labels += len(s.Labels)
	}
	if len(r.Segments) == 0 {
		return nil, errors.New("no segments")
	}
	first := r.Segments[0]
	p.Arity, p.ParameterTypes = first.Step.Arity, first.ParameterTypes
	for _, v := range p.Variables {
		if v.Immutable {
			enter := &p.Instructions[v.Declaration]
			enter.Args = append(enter.Args, v.Initial...)
		}
	}
	if labels == 0 {
		if len(r.Segments) != 1 || first.Step.Term != ir.TermReturn {
			return nil, errors.New("a function without labels must be one returning segment")
		}
		p.Result, p.ResultParts = first.Step.Result, first.Step.ResultParts
		return p, nil
	}
	scopeAt := func(position int) int {
		scope, enter := 0, -1
		for _, exit := range p.ScopeExits {
			if exit.Enter < position && position < exit.Position && exit.Enter > enter {
				scope, enter = exit.ID, exit.Enter
			}
		}
		return scope
	}
	c := &ir.Control{
		Labels: make([]bool, labels+1), LabelScopes: make([]int, labels+1), Params: make([]int, labels+1),
		ParamBits: make([][]uint8, labels+1), ParamTypes: make([][]ir.Type, labels+1),
	}
	c.Steps = append(c.Steps, ir.ControlStep{IsLabel: true, Label: labels})
	c.Labels[labels] = true
	for _, s := range r.Segments {
		var bits []uint8
		for _, ins := range s.Code {
			if ins.Op != ir.OpLabelParam {
				break
			}
			bits = append(bits, ins.Bits)
		}
		for _, label := range s.Labels {
			if label < 0 || int(label) >= labels || c.Labels[label] {
				return nil, fmt.Errorf("label %d is out of range or bound twice", label)
			}
			c.Labels[label], c.LabelScopes[label] = true, scopeAt(s.Base)
			if len(bits) != 0 {
				c.Params[label], c.ParamBits[label] = len(bits), bits
				c.ParamTypes[label] = make([]ir.Type, len(bits))
				for i, w := range bits {
					c.ParamTypes[label][i] = ir.Scalar(w)
				}
			}
			c.Steps = append(c.Steps, ir.ControlStep{IsLabel: true, Label: int(label)})
		}
		c.Steps = append(c.Steps, s.Step)
	}
	p.Control = c
	return p, nil
}

// Seal concatenates and seals the segments, as Finish reports failures.
func (r *SegmentRecorder) Seal() (*ir.SealedProgram, error) {
	p, err := r.Program()
	if err != nil {
		return nil, fmt.Errorf("segments: %w: %w", ErrInvalidFunction, err)
	}
	sealed, err := ir.SealOwned(p)
	if err != nil {
		return nil, fmt.Errorf("Finish: program validation failed: %w: %w", ErrInvalidFunction, err)
	}
	return sealed, nil
}

// SetSegmentMode makes NewBuilder return segment Builders whose Finish
// returns the sealed concatenation of their segments, until restore runs.
// Each one's recorder is passed to observe, when it is not nil.
func SetSegmentMode(observe func(*SegmentRecorder)) (restore func()) {
	old := testSegmentHook
	testSegmentHook = func() (func(*ir.Segment) error, func(error) (*ir.SealedProgram, error)) {
		r := NewSegmentRecorder()
		if observe != nil {
			observe(r)
		}
		return r.Emit, func(endErr error) (*ir.SealedProgram, error) {
			if endErr != nil {
				return nil, endErr
			}
			return r.Seal()
		}
	}
	return func() { testSegmentHook = old }
}

// ProgramDifference describes the first difference between two programs,
// or returns "". Nil and empty lists are equal.
func ProgramDifference(a, b *ir.Program) string {
	ca, cb := canonicalProgram(a), canonicalProgram(b)
	if len(ca.Instructions) != len(cb.Instructions) {
		return fmt.Sprintf("%d instructions, want %d", len(cb.Instructions), len(ca.Instructions))
	}
	for i := range ca.Instructions {
		if !reflect.DeepEqual(ca.Instructions[i], cb.Instructions[i]) {
			return fmt.Sprintf("instruction %d: %+v, want %+v", i, cb.Instructions[i], ca.Instructions[i])
		}
	}
	for _, field := range []struct {
		name string
		x, y any
	}{
		{"Variables", ca.Variables, cb.Variables}, {"ScopeExits", ca.ScopeExits, cb.ScopeExits},
		{"Arity", ca.Arity, cb.Arity}, {"Result", ca.Result, cb.Result}, {"ResultParts", ca.ResultParts, cb.ResultParts},
		{"ParameterTypes", ca.ParameterTypes, cb.ParameterTypes}, {"Control", ca.Control == nil, cb.Control == nil},
	} {
		if !reflect.DeepEqual(field.x, field.y) {
			return fmt.Sprintf("%s: %+v, want %+v", field.name, field.y, field.x)
		}
	}
	if ca.Control == nil {
		return ""
	}
	x, y := ca.Control, cb.Control
	for _, field := range []struct {
		name string
		x, y any
	}{
		{"Labels", x.Labels, y.Labels}, {"LabelScopes", x.LabelScopes, y.LabelScopes}, {"Params", x.Params, y.Params},
		{"ParamBits", x.ParamBits, y.ParamBits}, {"ParamTypes", x.ParamTypes, y.ParamTypes},
	} {
		if !reflect.DeepEqual(field.x, field.y) {
			return fmt.Sprintf("Control.%s: %+v, want %+v", field.name, field.y, field.x)
		}
	}
	if len(x.Steps) != len(y.Steps) {
		return fmt.Sprintf("%d steps, want %d", len(y.Steps), len(x.Steps))
	}
	for i := range x.Steps {
		if !reflect.DeepEqual(x.Steps[i], y.Steps[i]) {
			return fmt.Sprintf("step %d: %+v, want %+v", i, y.Steps[i], x.Steps[i])
		}
	}
	return ""
}

func canonicalProgram(p *ir.Program) *ir.Program {
	p = p.Clone()
	ints := func(v []int) []int {
		if len(v) == 0 {
			return nil
		}
		return v
	}
	for i := range p.Instructions {
		ins := &p.Instructions[i]
		ins.Args = ints(ins.Args)
		if len(ins.Data) == 0 {
			ins.Data = nil
		}
	}
	for i := range p.Variables {
		p.Variables[i].Initial = ints(p.Variables[i].Initial)
	}
	for i := range p.ScopeExits {
		p.ScopeExits[i].Hidden, p.ScopeExits[i].Exports = ints(p.ScopeExits[i].Hidden), ints(p.ScopeExits[i].Exports)
	}
	if len(p.Variables) == 0 {
		p.Variables = nil
	}
	if len(p.ScopeExits) == 0 {
		p.ScopeExits = nil
	}
	if len(p.ParameterTypes) == 0 {
		p.ParameterTypes = nil
	}
	p.ResultParts = ints(p.ResultParts)
	if c := p.Control; c != nil {
		for i := range c.ParamBits {
			if len(c.ParamBits[i]) == 0 {
				c.ParamBits[i] = nil
			}
		}
		for i := range c.ParamTypes {
			if len(c.ParamTypes[i]) == 0 {
				c.ParamTypes[i] = nil
			}
		}
		for i := range c.Steps {
			s := &c.Steps[i]
			s.Values, s.Args, s.OtherArgs, s.ResultParts = ints(s.Values), ints(s.Args), ints(s.OtherArgs), ints(s.ResultParts)
		}
	}
	return p
}
