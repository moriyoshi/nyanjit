//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package compile_test

import (
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"testing"
)

func TestVariableHomeWithCompactCounter(t *testing.T) {
	b := n.NewBuilder()
	x := b.ParamNative()
	v := b.Var(b.ConstNative(7))
	loop, done := b.NewLabel(), b.NewLabel()
	b.JumpArgs(loop, x)
	s := b.BindParamsBits(loop, []uint8{n.NativeBits})
	d := b.Sub(s[0], b.ConstNative(1))
	b.Set(v, b.Add(b.Get(v), b.ConstNative(2)))
	b.BranchArgs(b.CompareImmediate(d, n.BranchNotEqual, 0), loop, []n.Value{d}, done, nil)
	b.Bind(done)
	b.Return(b.Get(v))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, compact := range []bool{false, true} {
		c, err := compile.Compile(p, abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 8192, PreferCompactLoops: compact})
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
