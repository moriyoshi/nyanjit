package ir_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

// switchProgram is entry(s, x) switching on s to three blocks that take x,
// the cases repeating one of them.
func switchProgram(t *testing.T) *ir.Program {
	t.Helper()
	i64 := ir.Scalar(64)
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	s, x := pb.Param(entry, ir.Scalar(32)), pb.Param(entry, i64)
	// A vector the tests may switch on instead.
	pb.Append(entry, ir.Instruction{Op: ir.OpVSplat, Type: ir.Vec(2, 64), Bits: 64, A: x})
	a, b, c := pb.NewBlock(i64), pb.NewBlock(i64), pb.NewBlock(i64)
	pb.Switch(entry, s, []ir.Block{a, b, a}, c, x)
	for _, blk := range []ir.Block{a, b, c} {
		pb.Return(blk, pb.BlockParam(blk, 0))
	}
	sealed, err := pb.Seal()
	if err != nil {
		t.Fatal(err)
	}
	return sealed.Clone()
}

func switchStep(p *ir.Program) *ir.ControlStep {
	for i := range p.Control.Steps {
		if s := &p.Control.Steps[i]; !s.IsLabel && s.Term == ir.TermSwitch {
			return s
		}
	}
	return nil
}

func TestProgramBuilderSwitch(t *testing.T) {
	p := switchProgram(t)
	step := switchStep(p)
	if step == nil || len(step.Cases) != 3 || step.Cases[0] != step.Cases[2] || step.Selector != 0 || len(step.Args) != 1 {
		t.Fatalf("switch step %+v", step)
	}
	// The selector dominates every case block.
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	// Every destination takes Args, so their parameter lists must agree.
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	s := pb.Param(entry, ir.Scalar(64))
	one, none := pb.NewBlock(ir.Scalar(64)), pb.NewBlock()
	pb.Switch(entry, s, []ir.Block{one, none}, one, s)
	if _, err := pb.Seal(); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("mismatched destination parameters: %v", err)
	}
	pb = ir.NewProgramBuilder()
	entry = pb.Entry()
	s = pb.Param(entry, ir.Scalar(64))
	pb.Switch(entry, s, nil, entry)
	if _, err := pb.Seal(); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("switch without cases: %v", err)
	}
}

func TestSwitchValidation(t *testing.T) {
	cases := map[string]func(p *ir.Program, s *ir.ControlStep){
		"no cases":             func(p *ir.Program, s *ir.ControlStep) { s.Cases = nil },
		"case out of range":    func(p *ir.Program, s *ir.ControlStep) { s.Cases[1] = len(p.Control.Labels) },
		"negative case":        func(p *ir.Program, s *ir.ControlStep) { s.Cases[0] = -1 },
		"selector not a value": func(p *ir.Program, s *ir.ControlStep) { s.Selector = 99 },
		"Result set":           func(p *ir.Program, s *ir.ControlStep) { s.Result = 1 },
		"Predicate set": func(p *ir.Program, s *ir.ControlStep) {
			s.Predicate = &ir.PredicateExpr{Kind: ir.PredicateCompare, Value: 0}
		},
		"Other set":           func(p *ir.Program, s *ir.ControlStep) { s.Other = s.To },
		"case without params": func(p *ir.Program, s *ir.ControlStep) { s.Cases[0] = p.Control.Steps[0].Label },
		"selector on a jump":  func(p *ir.Program, s *ir.ControlStep) { s.Term = ir.TermJump },
		"vector selector":     func(p *ir.Program, s *ir.ControlStep) { s.Selector = 2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := switchProgram(t)
			mutate(p, switchStep(p))
			if err := p.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatalf("got %v, want ErrInvalidProgram", err)
			}
		})
	}
	// Cases on another terminator are rejected too.
	p := switchProgram(t)
	for i := range p.Control.Steps {
		if s := &p.Control.Steps[i]; !s.IsLabel && s.Term == ir.TermReturn {
			s.Cases = []int{0}
			break
		}
	}
	if err := p.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("Cases on a return: %v", err)
	}
}

// Each case is one predicate node, per switch and in the program's total.
func TestSwitchPredicateBudget(t *testing.T) {
	build := func(n int, extra bool) error {
		pb := ir.NewProgramBuilder()
		entry := pb.Entry()
		s := pb.Param(entry, ir.Scalar(64))
		target := pb.NewBlock()
		list := make([]ir.Block, n)
		for i := range list {
			list[i] = target
		}
		from := entry
		if extra {
			next := pb.NewBlock()
			pb.Branch(entry, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: s}, next, nil, target, nil)
			from = next
		}
		pb.Switch(from, s, list, target)
		pb.Return(target, s)
		_, err := pb.Seal()
		return err
	}
	if err := build(ir.MaxPredicateNodes, false); err != nil {
		t.Fatalf("switch at the limit: %v", err)
	}
	if err := build(ir.MaxPredicateNodes+1, false); !errors.Is(err, ir.ErrPredicateLimit) {
		t.Fatalf("switch above the limit: %v", err)
	}
	if err := build(ir.MaxPredicateNodes, true); !errors.Is(err, ir.ErrPredicateLimit) {
		t.Fatalf("program total above the limit: %v", err)
	}
}
