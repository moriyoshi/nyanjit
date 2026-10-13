package compile_test

import (
	"errors"
	"fmt"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestScopedParameterInitializersAcrossTargets(t *testing.T) {
	for _, architecture := range predicateFuzzArchs {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			for arity := 0; arity <= 8; arity++ {
				for initial := 0; initial < max(1, arity); initial++ {
					t.Run(fmt.Sprintf("%s/%d/%d/%d", architecture, flavor, arity, initial), func(t *testing.T) {
						// Raw IR avoids the host Builder's own entry-arity limit.
						p := &ir.Program{Arity: arity}
						for i := 0; i < arity; i++ {
							p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpParam})
						}
						if arity == 0 {
							p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpConst})
						} else {
							p.Variables = []ir.Variable{{Type: ir.Scalar(64), Scope: 1, Region: 1, Declaration: arity + 1, Initial: []int{initial}}}
							p.Instructions = append(p.Instructions, ir.Instruction{Op: ir.OpScopeEnter, Imm: 1}, ir.Instruction{Op: ir.OpSet, Variable: 0, A: initial, Bits: 64}, ir.Instruction{Op: ir.OpGet, Variable: 0, Bits: 64}, ir.Instruction{Op: ir.OpScopeLeave, Imm: 1})
							p.ScopeExits = []ir.ScopeExit{{ID: 1, Enter: arity, Position: arity + 4, Exports: []int{arity + 2}}}
						}
						p.Result = len(p.Instructions) - 1
						if arity != 0 {
							p.Result--
						}
						if err := p.Validate(); err != nil {
							t.Fatal(err)
						}
						_, err := lowerListing(p, compile.TargetSpec{Architecture: architecture}, compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 16}})
						if arity > abi.MaxArgumentsFor(architecture) {
							if !errors.Is(err, compile.ErrBackendLimit) {
								t.Fatalf("excess arity: %v", err)
							}
						} else if err != nil {
							t.Fatal(err)
						}
					})
				}
			}
		}
	}
}

func TestControlParameterPlacementAndArity(t *testing.T) {
	b := n.NewBuilder()
	x := b.Param()
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(b.IsZero(x), yes)
	b.Jump(no)
	b.Bind(yes)
	b.Return(b.Const(1))
	b.Bind(no)
	b.Return(b.Const(2))
	s, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	base := s.Clone()
	for _, arity := range []int{-1, 0, 2, 100} {
		p := base.Clone()
		p.Control.Steps[1].Arity = arity
		assertInvalidAllEntries(t, p)
	}
	p := base.Clone()
	p.Instructions[0].Op = ir.OpConst
	p.Arity = 0
	assertInvalidAllEntries(t, p)
	p = base.Clone()
	for i, step := range p.Control.Steps {
		if i > 1 && !step.IsLabel {
			p.Instructions[step.Start].Op = ir.OpParam
			p.Control.Steps[i].Arity = 1
			p.Arity++
			break
		}
	}
	assertInvalidAllEntries(t, p)
}

func assertInvalidAllEntries(t *testing.T, p *ir.Program) {
	t.Helper()
	if err := p.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("Validate: %v", err)
	}
	if _, err := ir.Seal(p); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("Seal: %v", err)
	}
	for _, arch := range predicateFuzzArchs {
		spec := compile.TargetSpec{Architecture: arch}
		external := compile.Func{Context: compile.ContextExternal}
		_, err := lowerListing(p, spec, compile.Func{})
		if !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("%s Listing: %v", arch, err)
		}
		_, err = lowerUnit(p, spec, external)
		if !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("%s Lower: %v", arch, err)
		}
		_, err = lowerTemplate(p, spec, compile.Func{})
		if !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("%s Template: %v", arch, err)
		}
		_, err = lowerReport(p, spec, external)
		if !errors.Is(err, compile.ErrInvalidFunction) {
			t.Fatalf("%s Report: %v", arch, err)
		}
	}
}

func TestNonValueResultsAndScopeExitOperands(t *testing.T) {
	b := n.NewBuilder()
	x := b.Param()
	b.Enter()
	v := b.Var(x)
	b.Store64(x, x)
	b.Store8(x, x)
	b.VStore(x, b.Splat(ir.Vec(2, 64), x))
	b.Leave(b.Get(v))
	b.Return(x)
	s, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	base := s.Clone()
	for id, ins := range base.Instructions {
		if !ins.Op.ProducesValue() {
			p := base.Clone()
			p.Result = id
			assertInvalidAllEntries(t, p)
			for field := 0; field < 2; field++ {
				p = base.Clone()
				if field == 0 {
					p.ScopeExits[0].Hidden = append(p.ScopeExits[0].Hidden, id)
				} else {
					p.ScopeExits[0].Exports = append(p.ScopeExits[0].Exports, id)
				}
				assertInvalidAllEntries(t, p)
			}
		}
	}

}

// A value created inside this scope after one of its labels is a valid local
// export. Entry parameters outside the scope are not owned by it.
func TestScopeExportsPermitOwnedLabelInputs(t *testing.T) {
	b := n.NewBuilder()
	x := b.Param()
	b.Enter()
	l := b.NewLabel()
	b.Jump(l)
	b.Bind(l)
	y := b.Add(x, x)
	b.Leave(y)
	b.Return(y)
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range predicateFuzzArchs {
		if _, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 16}}); err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
	}
}

// TestScopeExitExportsFollowDominance exports values from blocks that
// dominate the exit segment: they are valid and must lower on every target.
func TestScopeExitExportsFollowDominance(t *testing.T) {
	b := n.NewBuilder()
	x := b.Param()
	v := b.Var(x)
	head := b.NewLabel()
	b.Jump(head)
	b.Bind(head)
	// y is defined after a label and dominates the exit segment.
	y := b.Add(x, b.Const(1))
	b.Set(v, y)
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(b.IsZero(b.Get(v)), yes)
	b.Jump(no)
	b.Bind(yes)
	b.Enter()
	g := b.Get(v)
	b.Leave(g)
	b.Return(g)
	b.Bind(no)
	b.Return(b.Const(2))
	sealed, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	base := sealed.Clone()
	for id, ins := range base.Instructions {
		if ins.Op == ir.OpParam || ins.Op == ir.OpLabelParam || ins.Op == ir.OpAdd {
			p := base.Clone()
			p.ScopeExits[0].Exports = append(p.ScopeExits[0].Exports, id)
			if err := p.Validate(); err != nil {
				t.Fatalf("export of dominating value %d: %v", id, err)
			}
			for _, arch := range predicateFuzzArchs {
				if _, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{}); err != nil {
					t.Fatalf("%s: export of dominating value %d: %v", arch, id, err)
				}
			}
		}
	}
}
func TestScopeVectorAliasesPreserveEarlierValues(t *testing.T) {
	for _, exported := range []bool{false, true} {
		b := n.NewBuilder()
		x := b.Param()
		b.Enter()
		v := b.Splat(ir.Vec(2, 64), x)
		if exported {
			b.Leave(v)
			b.Return(b.ExtractLane(v, 0))
		} else {
			b.Leave()
			b.Return(x)
		}
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		for _, arch := range predicateFuzzArchs {
			if _, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 16}}); err != nil {
				t.Fatalf("%s export=%v: %v", arch, exported, err)
			}
		}
	}
}
