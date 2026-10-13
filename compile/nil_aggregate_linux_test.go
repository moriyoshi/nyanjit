//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"errors"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

func checkNilAggregate[T any](t *testing.T) {
	t.Helper()
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		b := n.NewBuilder()
		n.PointerParam[T](b)
		b.Return(b.Const(42)) // no dereference: safe on the unfixed tree
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		var pointer *T
		for _, pair := range []bool{false, true} {
			func() {
				defer func() {
					e, ok := recover().(error)
					if !ok || !errors.Is(e, execution.ErrArgumentType) {
						t.Errorf("nil Words accepted: %v", e)
					}
				}()
				if pair {
					c.CallPair(execution.Words(pointer)...)
				} else {
					c.Call(execution.Words(pointer)...)
				}
			}()
		}
		// Raw words deliberately bypass logical type/nil validation. The
		// fixture returns a constant, so a raw zero address is safe here.
		if got, err := c.Call(0); err != nil || got != 42 {
			t.Fatalf("raw zero rejected: %d %v", got, err)
		}
		if got, err := c.Call(execution.Words(new(T))...); err != nil || got != 42 {
			t.Fatalf("valid pointer: %d %v", got, err)
		}
		if flavor == abi.Go {
			var f func(*T) uint64
			err := c.Func(&f)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					err, ok := recover().(error)
					if !ok || !errors.Is(err, execution.ErrArgumentType) {
						t.Errorf("nil Func accepted: %v", err)
					}
				}()
				f(nil)
			}()
			c.Close()
			if got := f(new(T)); got != 42 {
				t.Fatalf("Func wrapper changed lifetime: %d", got)
			}
		}
	}
}
func TestNilAggregatePointerRejected(t *testing.T) {
	t.Run("struct", func(t *testing.T) { checkNilAggregate[struct{ A uint64 }](t) })
	t.Run("array", func(t *testing.T) { checkNilAggregate[[8]uint32](t) })
}
