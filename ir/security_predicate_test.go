package ir

import (
	"errors"
	"testing"
)

func TestSecurityProgramPredicateBudget(t *testing.T) {
	leaf := &PredicateExpr{Kind: PredicateCompare, Value: 0}
	dag := leaf
	for i := 0; i < 12; i++ {
		dag = &PredicateExpr{Kind: PredicateAnd, Left: dag, Right: dag}
	}
	if err := validatePredicateLimits(dag); err != nil {
		t.Fatal(err)
	}
	// Every individual branch is within budget; the complete program is not.
	p := &Program{Result: -1, Control: &Control{Labels: []bool{true, true, true, true, true}, LabelScopes: make([]int, 5), Params: make([]int, 5), ParamBits: make([][]uint8, 5)}}
	for i := 0; i < 5; i++ {
		p.Instructions = append(p.Instructions, Instruction{Op: OpConst, Bits: 64})
		p.Control.Steps = append(p.Control.Steps, ControlStep{IsLabel: true, Label: i})
		step := ControlStep{Start: i, End: i + 1, Result: i, Term: TermReturn, To: -1, Other: -1}
		if i < 4 {
			step.Values = []int{i}
			step.Term = TermBranch
			step.To = i + 1
			step.Other = i + 1
			step.Predicate = &PredicateExpr{Kind: PredicateCompare, Value: i}
		}
		p.Control.Steps = append(p.Control.Steps, step)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("invalid test fixture: %v", err)
	}
	for i := range p.Control.Steps {
		if p.Control.Steps[i].Predicate == nil {
			continue
		}
		d := p.Control.Steps[i].Predicate
		for k := 0; k < 12; k++ {
			d = &PredicateExpr{Kind: PredicateAnd, Left: d, Right: d}
		}
		p.Control.Steps[i].Predicate = d
	}
	if err := p.Validate(); !errors.Is(err, ErrPredicateLimit) {
		t.Fatalf("missing program-wide budget: %v", err)
	}
	// One compact DAG must also fail before its expanded traversal is allocated.
	for i := 0; i < 8; i++ {
		dag = &PredicateExpr{Kind: PredicateAnd, Left: dag, Right: dag}
	}
	if err := validatePredicateLimits(dag); !errors.Is(err, ErrPredicateLimit) {
		t.Fatalf("large DAG accepted: %v", err)
	}
}
