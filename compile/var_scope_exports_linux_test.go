//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func leaveExport(outer, inner int) (func(b *n.Builder), uintptr) {
	build := func(b *n.Builder) {
		x := b.ParamNative()
		b.Enter()
		vs := []n.Var{}
		for i := 0; i < outer; i++ {
			vs = append(vs, b.Var(b.Add(x, b.ConstNative(uintptr(i*3)))))
		}
		b.Enter()
		ws := []n.Var{}
		for i := 0; i < inner; i++ {
			ws = append(ws, b.Var(b.ConstNative(uintptr(1000+i))))
		}
		t := b.ConstNative(0)
		for _, w := range ws {
			t = b.Xor(b.Mul(t, b.ConstNative(31)), b.Get(w))
		}
		s := b.Get(ws[len(ws)-1])
		b.Leave(s, t)
		r := b.Add(b.Mul(t, b.ConstNative(7)), s)
		for _, v := range vs {
			r = b.Xor(b.Mul(r, b.ConstNative(31)), b.Get(v))
		}
		b.Leave(r)
		b.Return(r)
	}
	x := uint64(5)
	t := uint64(0)
	for i := 0; i < inner; i++ {
		t = t*31 ^ uint64(1000+i)
	}
	r := t*7 + uint64(1000+inner-1)
	for i := 0; i < outer; i++ {
		r = r*31 ^ (x + uint64(i*3))
	}
	return build, uintptr(r)
}

func TestScopedVariableExportGrid(t *testing.T) {
	for outer := 1; outer < 30; outer++ {
		for inner := 1; inner < 30; inner++ {
			build, want := leaveExport(outer, inner)
			b := n.NewBuilder()
			build(b)
			p, e := testFinishRaw(b)
			if e != nil {
				t.Fatal(e)
			}
			c, e := n.Compile(p, compile.TargetSpec{}, compile.Func{Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
			if e != nil {
				t.Fatal(e)
			}
			got, _ := c.Call(5)
			c.Close()
			if got != want {
				t.Errorf("outer %d inner %d got %#x want %#x", outer, inner, got, want)
			}
		}
	}
}
