//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func limitProg(nv int) func(b *n.Builder) {
	return func(b *n.Builder) {
		x := b.ParamNative()
		b.Enter()
		vs := []n.Var{}
		for i := 0; i < nv; i++ {
			vs = append(vs, b.Var(b.Add(x, b.ConstNative(uintptr(i)))))
		}
		loop, done := b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(vs[0])), done)
		b.Set(vs[1], b.Add(b.Mul(b.ConstNative(3), b.ConstNative(8)), b.Mul(b.Get(vs[2]), b.ConstNative(5))))
		b.Set(vs[0], b.Sub(b.Get(vs[0]), b.ConstNative(1)))
		b.Jump(loop)
		b.Bind(done)
		t := b.ConstNative(0)
		for _, v := range vs {
			t = b.Xor(b.Mul(t, b.ConstNative(31)), b.Get(v))
		}
		b.Leave(t)
		b.Return(t)
	}
}

func TestScopedVariableHomeCapacity(t *testing.T) {
	arches := []string{""}

	for _, arch := range arches {
		for nv := 3; nv < 40; nv++ {
			b := n.NewBuilder()
			limitProg(nv)(b)
			p, e := b.Finish()
			if e != nil {
				t.Fatal(e)
			}
			_, e = compile.EmitAssembly(p, abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 8192, Architecture: arch})
			if e != nil {
				t.Errorf("%s nv=%d: %v", arch, nv, e)
			}
		}
	}
}
