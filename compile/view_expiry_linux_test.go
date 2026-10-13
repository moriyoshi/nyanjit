//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"reflect"
	"runtime"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

type expiryHolder struct{ Items []uint64 }

func replaceExpiryItems(p *expiryHolder) uint64 { p.Items = []uint64{999}; runtime.GC(); return 0 }

func TestIndexedViewLoopBackEdgeRejected(t *testing.T) {
	b := n.NewBuilder()
	root := n.PointerParam[expiryHolder](b)
	sum := b.Var(b.Const(0))
	count := b.Var(b.Const(0))
	bad, loop, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	elem := root.At("Items").Index(b.ConstNative(0), bad)
	// The view dominates the loop, but it is block-local: using it after
	// the back edge, behind a callback, is rejected.
	b.Jump(loop)
	b.Bind(loop)
	b.Set(sum, b.Add(b.Get(sum), elem.Load()))
	b.CallGo(b.ConstNative(reflect.ValueOf(replaceExpiryItems).Pointer()), root.Base())
	b.Set(count, b.Add(b.Get(count), b.Const(1)))
	b.Branch(b.CompareImmediate(b.Get(count), ir.BranchBelow, 2), loop)
	b.Jump(done)
	b.Bind(done)
	b.Return(b.Get(sum))
	b.Bind(bad)
	b.Return(b.Const(0))
	if _, err := b.Finish(); err == nil {
		t.Fatal("indexed loop borrow accepted across a callback back-edge")
	}
}
func TestIndexedViewLoopReindexesAfterCallback(t *testing.T) {
	b := n.NewBuilder()
	root := n.PointerParam[expiryHolder](b)
	sum := b.Var(b.Const(0))
	count := b.Var(b.Const(0))
	bad, loop, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Jump(loop)
	b.Bind(loop)
	elem := root.At("Items").Index(b.ConstNative(0), bad)
	b.Set(sum, b.Add(b.Get(sum), elem.Load()))
	b.CallGo(b.ConstNative(reflect.ValueOf(replaceExpiryItems).Pointer()), root.Base())
	b.Set(count, b.Add(b.Get(count), b.Const(1)))
	b.Branch(b.CompareImmediate(b.Get(count), ir.BranchBelow, 2), loop)
	b.Jump(done)
	b.Bind(done)
	b.Return(b.Get(sum))
	b.Bind(bad)
	b.Return(b.Const(0))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}, AssumePointerFree: true})
	if managedUnsupported(err) {
		t.Skip("Go-managed code needs goregister:", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	input := &expiryHolder{Items: []uint64{7}}
	lo, hi, err := c.CallPair(execution.Words(input)...)
	if got := execution.Wide(lo, hi); err != nil || got != 1006 {
		t.Fatalf("re-indexed sum %d, %v; want 1006", got, err)
	}
}
