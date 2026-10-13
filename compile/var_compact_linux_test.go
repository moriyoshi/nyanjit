//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386)

package compile_test

import (
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestVariableHomeWithCompactCounter(t *testing.T) {
	b := n.NewBuilder()
	x := b.ParamNative()
	v := b.Var(b.ConstNative(7))
	counter := b.Var(x)
	loop, done := b.NewLabel(), b.NewLabel()
	b.Bind(loop)
	d := b.Sub(b.Get(counter), b.ConstNative(1))
	b.Set(v, b.Add(b.Get(v), b.ConstNative(2)))
	b.Set(counter, d)
	b.Branch(b.CompareImmediate(d, ir.BranchNotEqual, 0), loop)
	b.Bind(done)
	b.Return(b.Get(v))
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, compact := range []bool{false, true} {
		c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}, PreferCompactLoops: compact})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Call(5)
		c.Close()
		if err != nil || got != 17 {
			t.Fatalf("compact=%v: got %d, err %v", compact, got, err)
		}
	}
}
