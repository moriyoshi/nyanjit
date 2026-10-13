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
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := nyanjit.NewBuilder()
			c.build(b)
			_, err := b.Finish()
			var limit *compile.LimitError
			if !errors.As(err, &limit) || !errors.Is(err, compile.ErrBackendLimit) {
				t.Fatalf("got %v, want a LimitError", err)
			}
			if limit.Kind != c.kind || limit.Limit <= 0 || limit.Needed <= limit.Limit {
				t.Fatalf("got %+v, want %v above a positive limit", *limit, c.kind)
			}
		})
	}
}
