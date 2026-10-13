package builder_test

import (
	"errors"
	"testing"

	builder "github.com/moriyoshi/nyanjit/builder"
)

func TestVarScopeAndTypeErrors(t *testing.T) {
	first, second := builder.NewBuilder(), builder.NewBuilder()
	x, _ := first.ConstBits(32, 1)
	v, e := first.Var(x)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := second.Get(v); !errors.Is(e, builder.ErrInvalidFunction) {
		t.Fatalf("foreign Get:%v", e)
	}
	y, _ := second.ConstBits(32, 2)
	if e := second.Set(v, y); !errors.Is(e, builder.ErrInvalidFunction) {
		t.Fatalf("foreign Set:%v", e)
	}
	wide, _ := first.ConstBits(64, 2)
	if e := first.Set(v, wide); !errors.Is(e, builder.ErrInvalidFunction) {
		t.Fatalf("wrong type:%v", e)
	}
	if _, e := first.Get(builder.Var{}); !errors.Is(e, builder.ErrInvalidFunction) {
		t.Fatalf("zero Var:%v", e)
	}
	first.Enter()
	inside, e := first.Var(x)
	if e != nil {
		t.Fatal(e)
	}
	first.Leave()
	if _, e := first.Get(inside); !errors.Is(e, builder.ErrInvalidFunction) {
		t.Fatalf("expired Get:%v", e)
	}
	if e := first.Set(inside, x); !errors.Is(e, builder.ErrInvalidFunction) {
		t.Fatalf("expired Set:%v", e)
	}
	if _, e := first.Get(v); e != nil {
		t.Fatal(e)
	}
}
func TestVarRequiresScopeEntrySequence(t *testing.T) {
	b := builder.NewBuilder()
	l := b.NewLabel()
	b.Jump(l)
	b.Bind(l)
	x, _ := b.ConstNative(1)
	if _, e := b.Var(x); !errors.Is(e, builder.ErrInvalidFunction) {
		t.Fatalf("late Var:%v", e)
	}
	b.Enter()
	if _, e := b.Var(x); e != nil {
		t.Fatalf("new scope entry:%v", e)
	}
}
func TestGetSnapshotAndCaptureCrossBlocks(t *testing.T) {
	b := builder.NewBuilder()
	x, _ := b.ParamNative()
	v, _ := b.Var(x)
	snapshot, _ := b.Get(v)
	b.Enter()
	l := b.NewLabel()
	b.Jump(l)
	b.Bind(l)
	if !b.Valid(x) || !b.Valid(snapshot) {
		t.Fatal("pre-Enter values must be captures")
	}
	local, _ := b.Get(v)
	next := b.NewLabel()
	b.Jump(next)
	b.Bind(next)
	if !b.Valid(local) {
		t.Fatal("a dominating Get snapshot must stay usable in later blocks")
	}
	sum, e := b.Add(x, snapshot)
	if e != nil {
		t.Fatal(e)
	}
	b.Leave(sum)
	// The capture expires at Leave, but x itself is a root value whose
	// definition dominates this block.
	if !b.Valid(x) {
		t.Fatal("dominating root value became invalid after Leave")
	}
	b.Return(sum)
	p, e := testFinishRaw(b)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Variables) != 3 {
		t.Fatalf("vars+captures:%+v", p.Variables)
	}
	clone := p.Clone()
	clone.Variables[0].Initial[0] = -1
	if p.Variables[0].Initial[0] < 0 {
		t.Fatal("clone aliases variables")
	}
}
