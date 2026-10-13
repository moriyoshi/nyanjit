package builder_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestEnterTerminatesBeforeLoopLabel(t *testing.T) {
	b := builder.NewBuilder()
	n, _ := b.ParamNative()
	i, _ := b.Var(n)
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	loop, done := b.NewLabel(), b.NewLabel()
	if err := b.Bind(loop); err != nil {
		t.Fatal(err)
	}
	x, _ := b.Get(i)
	predicate, _ := b.IsZero(x)
	if err := b.Branch(predicate, done); err != nil {
		t.Fatal(err)
	}
	if err := b.Jump(loop); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(done); err != nil {
		t.Fatal(err)
	}
	result, _ := b.Get(i)
	if err := b.Leave(result); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	// The entry block jumps to the loop label, which the next step binds.
	entry, header := p.Control.Steps[1], p.Control.Steps[2]
	if entry.Term != ir.TermJump || !header.IsLabel || entry.To != header.Label || entry.End != p.ScopeExits[0].Enter+1 {
		t.Fatalf("Enter must end entry block: %+v", entry)
	}
}

func TestEnterWithoutBranchKeepsStraightLineValues(t *testing.T) {
	b := builder.NewBuilder()
	x, _ := b.ParamNative()
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	y, _ := b.Add(x, x)
	if err := b.Leave(y); err != nil {
		t.Fatal(err)
	}
	z, err := b.Add(x, y)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(z); err != nil {
		t.Fatal(err)
	}
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Control != nil || len(p.Variables) != 0 {
		t.Fatalf("straight-line Enter introduced control or captures: %+v", p)
	}
}

func TestEnterBoundaryWithBodyBeforeBranch(t *testing.T) {
	b := builder.NewBuilder()
	x, _ := b.ParamNative()
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	// Reserving the label after the calculation exercises a late transition
	// from a straight-line scope to a scope containing control flow.
	y, _ := b.Add(x, x)
	v, _ := b.Var(y)
	done := b.NewLabel()
	predicate, _ := b.IsZero(x)
	if err := b.Branch(predicate, done); err != nil {
		t.Fatal(err)
	}
	if err := b.Jump(done); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(done); err != nil {
		t.Fatal(err)
	}
	result, _ := b.Get(v)
	if err := b.Leave(result); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	entry := p.Control.Steps[1]
	if entry.Term != ir.TermJump || entry.End != p.ScopeExits[0].Enter+1 {
		t.Fatalf("late branch did not split at Enter: %+v", entry)
	}
	// x crosses the split by dominance: the scope's block uses it directly,
	// without a capture.
	if len(p.Variables) != 1 || p.Variables[0].Immutable {
		t.Fatalf("expected only mutable v: %+v", p.Variables)
	}
	if add := p.Instructions[entry.End]; add.Op != ir.OpAdd || add.A != 0 || add.B != 0 {
		t.Fatalf("scope block does not use x directly: %+v", add)
	}
}

func TestBindImplicitEdgePreservesBlockLocalValues(t *testing.T) {
	for _, enter := range []bool{false, true} {
		b := builder.NewBuilder()
		if enter {
			if err := b.Enter(); err != nil {
				t.Fatal(err)
			}
		}
		x, _ := b.ConstNative(1)
		v, _ := b.Var(x)
		label := b.NewLabel()
		if err := b.Bind(label); err != nil {
			t.Fatalf("Bind after body (Enter=%v): %v", enter, err)
		}
		if !b.Valid(x) {
			t.Fatal("dominating value did not cross the implicit edge")
		}
		if _, err := b.Var(x); !errors.Is(err, builder.ErrInvalidFunction) {
			t.Fatalf("late variable declaration: %v", err)
		}
		result, err := b.Get(v)
		if err != nil {
			t.Fatal(err)
		}
		if enter {
			if err := b.Leave(result); err != nil {
				t.Fatal(err)
			}
		}
		if err := b.Return(result); err != nil {
			t.Fatal(err)
		}
		p, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		if p.Control.Steps[1].Term != ir.TermJump {
			t.Fatal("missing implicit edge")
		}
	}
}

func TestEnterClosesParametersWhenScopeBranches(t *testing.T) {
	b := builder.NewBuilder()
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	x, err := b.ParamNative()
	if err != nil {
		t.Fatal(err)
	}
	v, _ := b.Var(x)
	done := b.NewLabel()
	if err := b.Jump(done); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(done); err != nil {
		t.Fatal(err)
	}
	result, _ := b.Get(v)
	if err := b.Leave(result); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	if _, err := testFinishRaw(b); !errors.Is(err, builder.ErrInvalidFunction) {
		t.Fatalf("parameter following an entry terminator: %v", err)
	}
}
