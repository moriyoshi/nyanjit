package nyanjit_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// The Builder reports the limits it checks while building as LimitErrors.
func TestBuilderLimitErrors(t *testing.T) {
	cases := map[string]struct {
		kind  compile.LimitKind
		build func(b *nyanjit.Builder)
	}{
		"predicate": {compile.LimitPredicateNodes, func(b *nyanjit.Builder) {
			x := b.Param()
			p := b.CompareImmediate(x, ir.BranchEqual, 1)
			for range 15 {
				p = p.And(p) // A shared subtree counts at every use.
			}
			done := b.NewLabel()
			b.Branch(p, done)
			b.Bind(done)
			b.Return(x)
		}},
		"program predicate budget": {compile.LimitPredicateNodes, func(b *nyanjit.Builder) {
			x := b.Param()
			labels := make([]nyanjit.Label, ir.MaxPredicateNodes+2)
			for i := range labels {
				labels[i] = b.NewLabel()
			}
			b.Jump(labels[0])
			for i := range len(labels) - 1 {
				b.Bind(labels[i])
				b.Branch(b.CompareImmediate(x, ir.BranchBelow, int64(i)), labels[i+1])
			}
			b.Bind(labels[len(labels)-1])
			b.Return(x)
		}},
		"switch cases": {compile.LimitPredicateNodes, func(b *nyanjit.Builder) {
			switchOverSameLabel(b, ir.MaxPredicateNodes+1, false)
		}},
		"switch program budget": {compile.LimitPredicateNodes, func(b *nyanjit.Builder) {
			switchOverSameLabel(b, ir.MaxPredicateNodes, true)
		}},
		"entry parameters": {compile.LimitArguments, func(b *nyanjit.Builder) {
			for range 1000 {
				b.Param()
			}
		}},
		"system call": {compile.LimitArguments, func(b *nyanjit.Builder) {
			x := b.Param()
			b.Return(b.Syscall(x, x, x, x, x, x, x, x))
		}},
		"call": {compile.LimitArguments, func(b *nyanjit.Builder) {
			x := b.Param()
			args := make([]nyanjit.Value, 1000)
			for i := range args {
				args[i] = x
			}
			b.Return(b.CallC(b.CastNative(b.Symbol("f")), args...))
		}},
	}
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := nyanjit.NewBuilder()
			c.build(b)
			_, err := b.Finish()
			checkBuilderLimit(t, "Finish", err, c.kind)
			// A Builder bound to a Target checks the same limits in Lower.
			b = nyanjit.NewBuilderFor(target, compile.Func{})
			c.build(b)
			_, err = b.Lower()
			checkBuilderLimit(t, "Lower", err, c.kind)
		})
	}
}

// switchOverSameLabel switches over n cases that all continue at one label;
// branch adds a one-node predicate before it.
func switchOverSameLabel(b *nyanjit.Builder, n int, branch bool) {
	x := b.Param()
	l := b.NewLabel()
	if branch {
		b.Branch(b.IsZero(x), l)
	}
	list := make([]nyanjit.Label, n)
	for i := range list {
		list[i] = l
	}
	b.Switch(x, list, l)
	b.Bind(l)
	b.Return(x)
}

// A Switch of exactly ir.MaxPredicateNodes cases is within the budget on
// both tiers.
func TestSwitchAtPredicateLimit(t *testing.T) {
	b := nyanjit.NewBuilder()
	switchOverSameLabel(b, ir.MaxPredicateNodes, false)
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Architecture: "amd64", Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := compile.Lower(target, program, compile.Func{}); err != nil {
			t.Errorf("tier %d: %v", tier, err)
		}
		bound := nyanjit.NewBuilderFor(target, compile.Func{})
		switchOverSameLabel(bound, ir.MaxPredicateNodes, false)
		if _, err := bound.Lower(); err != nil {
			t.Errorf("tier %d bound: %v", tier, err)
		}
	}
}

func checkBuilderLimit(t *testing.T, operation string, err error, kind compile.LimitKind) {
	t.Helper()
	var limit *compile.LimitError
	if !errors.As(err, &limit) || !errors.Is(err, compile.ErrBackendLimit) || errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("%s: got %v, want only a LimitError", operation, err)
	}
	if limit.Kind != kind || limit.Limit <= 0 || limit.Needed <= limit.Limit {
		t.Fatalf("%s: got %+v, want %v above a positive limit", operation, *limit, kind)
	}
}
