//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package compile_test

import (
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestVariableResultHomePreservesValues(t *testing.T) {
	cases := []struct {
		name  string
		build func(*n.Builder, n.Value, n.Var) n.Value
		want  func(uintptr) uintptr
	}{
		{"update", func(b *n.Builder, x n.Value, v n.Var) n.Value {
			b.Set(v, b.Add(b.Get(v), b.ConstNative(7)))
			return b.Get(v)
		}, func(x uintptr) uintptr { return x + 7 }},
		{"snapshot", func(b *n.Builder, x n.Value, v n.Var) n.Value {
			before := b.Get(v)
			b.Set(v, b.Add(before, b.ConstNative(7)))
			return b.Add(before, b.Get(v))
		}, func(x uintptr) uintptr { return 2*x + 7 }},
		{"original_argument", func(b *n.Builder, x n.Value, v n.Var) n.Value {
			b.Set(v, b.Add(b.Get(v), b.ConstNative(7)))
			return b.Add(x, b.Get(v))
		}, func(x uintptr) uintptr { return 2*x + 7 }},
		{"scope", func(b *n.Builder, x n.Value, v n.Var) n.Value {
			b.Enter()
			w := b.Var(b.ConstNative(3))
			b.Set(v, b.Mul(b.Get(v), b.Get(w)))
			b.Leave()
			return b.Add(b.Get(v), b.ConstNative(7))
		}, func(x uintptr) uintptr { return 3*x + 7 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := n.NewBuilder()
			x := b.ParamNative()
			v := b.Var(x)
			b.Return(tc.build(b, x, v))
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
				for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
					for _, harden := range []bool{false, true} {
						f := compile.Func{ABI: flavor, Harden: harden, Stack: compile.StackPolicy{Practice: stack}}
						if stack == abi.StackPlatform {
							f.Stack.BytesAvailable = 4096
							if flavor == abi.Go {
								f.Context = compile.ContextGoManaged
								f.AssumePointerFree = true
							} else {
								f.Context = compile.ContextForeign
							}
						}
						code, err := n.Compile(p, compile.TargetSpec{}, f)
						if managedUnsupported(err) {
							continue
						}
						if err != nil {
							t.Fatalf("%+v: %v", f, err)
						}
						for _, x := range []uintptr{0, 42, ^uintptr(0)} {
							got, err := code.Call(x)
							if err != nil || got != tc.want(x) {
								t.Errorf("%+v, x=%#x: got %#x, want %#x (%v)", f, x, got, tc.want(x), err)
							}
						}
						code.Close()
					}
				}
			}
		})
	}
}

func TestVariableResultHomeSurvivesCall(t *testing.T) {
	b := n.NewBuilder()
	x := b.ParamNative()
	b.Return(b.Add(x, b.ConstNative(10)))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	f := compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}
	callee, err := n.Compile(p, compile.TargetSpec{}, f)
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	address, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	b = n.NewBuilder()
	x = b.ParamNative()
	v := b.Var(x)
	b.Set(v, b.Add(b.Get(v), b.ConstNative(7)))
	called := b.CastNative(b.CallC(b.ConstNative(uintptr(address)), b.Get(v)))
	b.Return(b.Add(called, b.Get(v)))
	p, err = b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := n.Compile(p, compile.TargetSpec{}, f)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, x := range []uintptr{0, 42, ^uintptr(0)} {
		got, err := code.Call(x)
		if want := 2*(x+7) + 10; err != nil || got != want {
			t.Errorf("x=%#x: got %#x, want %#x (%v)", x, got, want, err)
		}
	}
}
