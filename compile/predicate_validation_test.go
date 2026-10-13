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

// Exhaust all node kinds, child shapes, and comparison field combinations on
// a valid branch fixture. In particular Compare.Left/Right and Not.Right must
// fail at Validate/Seal, before any compiler pass sees the predicate.
func TestPredicateValidationMatchesCompilation(t *testing.T) {
	b := n.NewBuilder()
	x, y := b.ParamBits(32), b.ParamBits(32)
	vx, vy := b.Var(x), b.Var(y)
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(b.IsZero(x), yes)
	b.Jump(no)
	b.Bind(yes)
	b.Return(b.Get(vx))
	b.Bind(no)
	b.Return(b.Get(vy))
	base, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	raw := base.Clone()
	branch := -1
	for i, step := range raw.Control.Steps {
		if step.Predicate != nil {
			branch = i
			break
		}
	}
	value := raw.Control.Steps[branch].Predicate.Value
	leaf := func() *ir.PredicateExpr { return &ir.PredicateExpr{Kind: ir.PredicateCompare, Value: value} }
	for kind := ir.PredicateKind(0); kind <= ir.PredicateOr+1; kind++ {
		for children := 0; children < 16; children++ {
			for fields := 0; fields < 8; fields++ {
				t.Run(fmt.Sprintf("kind%d/children%d/fields%d", kind, children, fields), func(t *testing.T) {
					p := raw.Clone()
					node := &ir.PredicateExpr{Kind: kind, Value: value, Other: value, Anchor: value, WithValue: fields&1 != 0, HasAnchor: fields&2 != 0}
					if fields&4 != 0 {
						node.Condition = ir.BranchAbove + 1
					}
					child := func(shape int) *ir.PredicateExpr {
						switch shape {
						case 1:
							return leaf()
						case 2:
							return node
						case 3:
							return &ir.PredicateExpr{Kind: ir.PredicateKind(255)}
						}
						return nil
					}
					node.Left, node.Right = child(children&3), child(children>>2)
					p.Control.Steps[branch].Predicate = node
					validErr := p.Validate()
					_, sealErr := ir.Seal(p)
					if (validErr == nil) != (sealErr == nil) {
						t.Fatalf("Validate %v Seal %v", validErr, sealErr)
					}
					stray := kind == ir.PredicateCompare && (node.Left != nil || node.Right != nil) || kind == ir.PredicateNot && node.Right != nil
					if stray && !errors.Is(validErr, ir.ErrInvalidProgram) {
						t.Fatalf("stray children accepted: %v", validErr)
					}
					for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
						_, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Context: compile.ContextExternal})
						if validErr == nil && err != nil {
							t.Fatalf("%s: validated shape rejected: %v", arch, err)
						}
						if validErr != nil && !errors.Is(err, compile.ErrInvalidFunction) {
							t.Fatalf("%s: invalid shape error %v", arch, err)
						}
					}
				})
			}
		}
	}
}

func TestPredicateOperandDiagnostics(t *testing.T) {
	b := n.NewBuilder()
	x, y := b.Param(), b.Param()
	v := b.Var(y)
	b.Set(v, x)
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(b.Compare(x, ir.BranchEqual, y), yes)
	b.Jump(no)
	b.Bind(yes)
	b.Return(b.Get(v))
	b.Bind(no)
	b.Return(b.Get(v))
	sealed, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	base := sealed.Clone()
	index := 0
	for i, step := range base.Control.Steps {
		if step.Predicate != nil {
			index = i
			break
		}
	}
	for _, operand := range []string{"value", "other", "anchor", "set", "stray"} {
		t.Run(operand, func(t *testing.T) {
			p := base.Clone()
			step := &p.Control.Steps[index]
			node := step.Predicate
			switch operand {
			case "value":
				step.Values = nil
			case "other":
				node.Value = node.Other
				step.Values = []int{node.Value}
				node.Other = 0
			case "anchor":
				node.HasAnchor = true
				node.Anchor = 0
				node.Value = node.Other
				node.WithValue = false
				step.Values = []int{node.Value}
			case "set":
				for id := step.Start; id < step.End; id++ {
					if p.Instructions[id].Op == ir.OpSet {
						node.Value = id
						node.WithValue = false
						step.Values = []int{id}
						break
					}
				}
			case "stray":
				node.Left = &ir.PredicateExpr{Kind: ir.PredicateCompare}
			}
			err := p.Validate()
			if !errors.Is(err, ir.ErrInvalidProgram) || !strings.Contains(err.Error(), fmt.Sprintf("step %d", index)) || !strings.Contains(err.Error(), "predicate node 0:") {
				t.Fatalf("missing indexed diagnostic: %v", err)
			}
			for _, arch := range []string{"386", "amd64", "arm64", "loong64", "riscv64", "ppc64le"} {
				_, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Context: compile.ContextExternal})
				if !errors.Is(err, ir.ErrInvalidProgram) {
					t.Fatalf("%s lost validation identity: %v", arch, err)
				}
			}
		})
	}
}
