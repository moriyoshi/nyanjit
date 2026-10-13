//go:build linux && (amd64 || arm64 || 386 || riscv64 || loong64 || ppc64le)

package compile_test

import (
	"errors"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestSecurityAggregateTypeMismatch(t *testing.T) {
	type Big struct{ A [64]uint64 }
	type Small struct{ A [1]uint64 }
	b := n.NewBuilder()
	out := n.PointerParam[Big](b)
	out.At("A[40]").Store(b.Const(123))
	b.Return(b.Const(0))
	p, e := b.Finish()
	if e != nil {
		t.Fatal(e)
	}
	opts := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}}
	c, e := n.Compile(p, compile.TargetSpec{}, opts)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	opts.ABI = abi.Go
	opts.Context = compile.ContextGoManaged
	opts.AssumePointerFree = true
	g, e := n.Compile(p, compile.TargetSpec{}, opts)
	if managedUnsupported(e) {
		t.Skip("Go-managed code needs goregister:", e)
	}
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	if e := g.Func(new(func(*Small) uint64)); !errors.Is(e, execution.ErrSignature) {
		t.Fatalf("wrong pointee accepted: %v", e)
	}
}

func TestSecurityTypedAggregateCall(t *testing.T) {
	type Big struct{ A [64]uint64 }
	b := n.NewBuilder()
	out := n.PointerParam[Big](b)
	out.At("A[40]").Store(b.Const(123))
	b.Return(b.Const(0))
	p, e := b.Finish()
	if e != nil {
		t.Fatal(e)
	}
	c, e := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	var target Big
	if _, e := c.Call(execution.Words(&target)...); e != nil {
		t.Fatal(e)
	}
	if target.A[40] != 123 {
		t.Fatal("matching call failed")
	}
}
