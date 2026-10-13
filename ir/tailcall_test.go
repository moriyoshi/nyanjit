package ir

import (
	"errors"
	"strings"
	"testing"
)

// tailProgram is f(target, x) = target(x, x) as a tail call; edit adjusts
// its one segment step before validation.
func tailProgram(edit func(p *Program, step *ControlStep)) *Program {
	p := &Program{Arity: 2, Instructions: []Instruction{{Op: OpParam}, {Op: OpParam, Bits: 32}}, Control: &Control{
		Labels: []bool{true}, LabelScopes: []int{0}, Params: []int{0}, ParamBits: [][]uint8{nil},
		Steps: []ControlStep{{IsLabel: true}, {Term: TermTailCall, End: 2, Arity: 2, Result: -1, To: -1, Other: -1, Args: []int{0, 1, 1}}},
	}}
	if edit != nil {
		edit(p, &p.Control.Steps[1])
	}
	return p
}

func TestTailCallValidation(t *testing.T) {
	if err := tailProgram(nil).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := tailProgram(func(p *Program, step *ControlStep) { step.Result, step.To = 0, 0 }).Validate(); err != nil {
		t.Errorf("zero Result and To: %v", err)
	}
	for name, edit := range map[string]func(*Program, *ControlStep){
		"no target":      func(_ *Program, s *ControlStep) { s.Args = nil },
		"edge":           func(_ *Program, s *ControlStep) { s.To = 1 },
		"narrow target":  func(p *Program, s *ControlStep) { s.Args[0] = 1; p.Instructions[1].Bits = 16 },
		"undefined":      func(_ *Program, s *ControlStep) { s.Args[1] = 5 },
		"other edge":     func(_ *Program, s *ControlStep) { s.Other = 1 },
		"result parts":   func(_ *Program, s *ControlStep) { s.ResultParts = []int{0, 1} },
		"branch payload": func(_ *Program, s *ControlStep) { s.OtherArgs = []int{0} },
	} {
		if err := tailProgram(edit).Validate(); !errors.Is(err, ErrInvalidProgram) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	// A tail call returns a scalar: a program may not also return pairs.
	wide := &Program{Arity: 2, Instructions: []Instruction{{Op: OpParam}, {Op: OpParam}}, Control: &Control{
		Labels: []bool{true, true}, LabelScopes: []int{0, 0}, Params: []int{0, 0}, ParamBits: [][]uint8{nil, nil},
		Steps: []ControlStep{
			{IsLabel: true},
			{Term: TermBranch, End: 2, Arity: 2, To: 1, Other: 1, Result: 0, Values: []int{0}, Predicate: &PredicateExpr{Kind: PredicateCompare, Value: 0, Condition: BranchEqual}},
			{IsLabel: true, Label: 1},
			{Term: TermReturn, Start: 2, End: 2, Result: 0, ResultParts: []int{0, 1}},
		},
	}}
	if err := wide.Validate(); err != nil {
		t.Fatalf("wide return program: %v", err)
	}
	wide.Control.Steps[1] = ControlStep{Term: TermTailCall, End: 2, Arity: 2, Result: -1, To: -1, Other: -1, Args: []int{0, 1}}
	wide.Control.Labels[1] = true
	if err := wide.Validate(); !errors.Is(err, ErrInvalidProgram) || !strings.Contains(err.Error(), "tail call") {
		t.Errorf("tail call with a wide return: %v", err)
	}
}
