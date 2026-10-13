package compile_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"

	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestPredicateNonValueOperands(t *testing.T) {
	bases := predicateFuzzBases(t)
	var base *ir.Program
	for _, fixture := range bases {
		if fixture.name == "nonvalues" {
			base = fixture.p
		}
	}
	if base == nil {
		t.Fatal("missing nonvalues fixture")
	}
	branch := -1
	for i, step := range base.Control.Steps {
		if step.Term == ir.TermBranch {
			branch = i
			break
		}
	}
	seen := map[ir.Opcode]bool{}
	for id := base.Control.Steps[branch].Start; id < base.Control.Steps[branch].End; id++ {
		op := base.Instructions[id].Op
		if op.ProducesValue() {
			continue
		}
		seen[op] = true
		for _, field := range []string{"Value", "Other", "Anchor", "Values", "Args", "OtherArgs"} {
			p := base.Clone()
			step := &p.Control.Steps[branch]
			switch field {
			case "Value":
				step.Predicate.Value = id
			case "Other":
				step.Predicate.Other = id
				step.Predicate.WithValue = true
			case "Anchor":
				step.Predicate.Anchor = id
				step.Predicate.HasAnchor = true
			case "Args":
				step.Args = append(step.Args, id)
			case "OtherArgs":
				step.OtherArgs = append(step.OtherArgs, id)
			}
			if field != "Args" && field != "OtherArgs" {
				step.Values = append(step.Values, id)
			}
			err := p.Validate()
			if !errors.Is(err, ir.ErrInvalidProgram) || !strings.Contains(err.Error(), fmt.Sprintf("step %d:", branch)) {
				t.Fatalf("opcode=%d field=%s accepted or missing step context: %v", op, field, err)
			}
			if (field == "Value" || field == "Other" || field == "Anchor") && !strings.Contains(err.Error(), "predicate node 0:") {
				t.Fatal("missing node/rule", err)
			}
			for _, arch := range predicateFuzzArchs {
				_, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Context: compile.ContextExternal})
				if !errors.Is(err, ir.ErrInvalidProgram) {
					t.Fatalf("%s opcode=%d %s: %v", arch, op, field, err)
				}
			}
		}
	}
	if len(seen) != 6 {
		t.Fatalf("fixture only covers %d non-value opcodes", len(seen))
	}
	// The same unique preorder index must identify this DAG's last node whether
	// rejection happens in the structural or operand validation pass.
	for _, badKind := range []bool{true, false} {
		p := base.Clone()
		step := &p.Control.Steps[branch]
		a := step.Predicate
		c := &ir.PredicateExpr{Kind: ir.PredicateCompare, Value: -1}
		if badKind {
			c.Kind = 255
		}
		step.Predicate = &ir.PredicateExpr{Kind: ir.PredicateAnd, Left: a, Right: &ir.PredicateExpr{Kind: ir.PredicateAnd, Left: a, Right: c}}
		err := p.Validate()
		if err == nil || !strings.Contains(err.Error(), "predicate node 3:") {
			t.Fatalf("DAG node numbering: %v", err)
		}
	}
}

// Exercise every opcode as an operand, including malformed raw instructions.
// The shape/type validator can reject them first; anything it accepts must be
// materializable by every compiler entry point.
func TestPredicateEveryOpcodeOperand(t *testing.T) {
	b := n.NewBuilder()
	_ = b.Param()
	operand := b.ConstNative(9)
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(b.IsZero(operand), yes)
	b.Jump(no)
	b.Bind(yes)
	b.Return(b.ConstNative(1))
	b.Bind(no)
	b.Return(b.ConstNative(0))
	sealed, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	base := sealed.Clone()
	branch := -1
	for i, step := range base.Control.Steps {
		if step.Term == ir.TermBranch {
			branch = i
			break
		}
	}
	for op := ir.OpParam; op <= ir.OpVCarrylessMul; op++ {
		p := base.Clone()
		step := &p.Control.Steps[branch]
		id := step.Predicate.Value
		p.Instructions[id].Op = op
		valid := p.Validate() == nil
		_, err := ir.Seal(p)
		if (err == nil) != valid {
			t.Fatalf("opcode %d Validate/Seal disagree", op)
		}
		for _, arch := range predicateFuzzArchs {
			_, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Context: compile.ContextExternal})
			if valid && predicateFuzzIsValidationErr(err) {
				t.Fatalf("opcode %d %s: validated then rejected: %v", op, arch, err)
			}
			if !valid && !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatalf("opcode %d %s lost error: %v", op, arch, err)
			}
		}
	}
}

func TestStepValuesOutsideSegmentDiagnostic(t *testing.T) {
	p := predicateFuzzBases(t)[0].p.Clone()
	index := -1
	for i, step := range p.Control.Steps {
		if step.Term == ir.TermBranch {
			index = i
			break
		}
	}
	p.Control.Steps[index].Values = append(p.Control.Steps[index].Values, len(p.Instructions)+1)
	err := p.Validate()
	if !errors.Is(err, ir.ErrInvalidProgram) || !strings.Contains(err.Error(), fmt.Sprintf("step %d:", index)) || !strings.Contains(err.Error(), "Values") {
		t.Fatalf("missing indexed Values diagnostic: %v", err)
	}
}
