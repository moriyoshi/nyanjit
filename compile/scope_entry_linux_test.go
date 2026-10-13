//go:build linux && (amd64 || 386 || arm64 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"runtime"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestScopeEntryControlFlow(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func(*n.Builder)
		args  []uintptr
		want  uintptr
	}{
		{"loop zero", scopeEntryLoop, []uintptr{0}, 0},
		{"loop one", scopeEntryLoop, []uintptr{1}, 1},
		{"loop many", scopeEntryLoop, []uintptr{100}, 5050},
		{"root loop zero", scopeEntryRootLoop, []uintptr{0}, 0},
		{"root loop one", scopeEntryRootLoop, []uintptr{1}, 1},
		{"root loop many", scopeEntryRootLoop, []uintptr{100}, 5050},
		{"nested capture zero", scopeEntryNested, []uintptr{0}, 0},
		{"nested capture nonzero", scopeEntryNested, []uintptr{7}, 28},
		{"nested branch only", scopeEntryNestedOnly, []uintptr{7}, 28},
		{"straight sibling", scopeEntrySibling, []uintptr{7}, 35},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := n.NewBuilder()
			c.build(b)
			p := varFinish(t, b)
			for _, stack := range []abi.StackPractice{abi.StackPlatform, abi.StackNoUse} {
				if got := varExecute(t, p, stack, c.args...); got != c.want {
					t.Fatalf("stack %v: got %d, want %d", stack, got, c.want)
				}
			}
		})
	}
}

func TestScopeEntryCrossTarget(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"} {
		t.Run(arch, func(t *testing.T) {
			for _, build := range []func(*n.Builder){scopeEntryLoop, scopeEntryRootLoop, scopeEntryNested, scopeEntryNestedOnly, scopeEntrySibling} {
				b := n.NewBuilder()
				build(b)
				p := varFinish(t, b)
				spec := compile.TargetSpec{Architecture: arch}
				o := compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}}
				if arch == runtime.GOARCH {
					external := o
					external.Context = compile.ContextExternal
					if _, err := lowerTemplate(p, spec, external); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := lowerListing(p, spec, o); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func scopeEntryLoop(b *n.Builder) {
	scopeEntryLoopBody(b, true)
}

func scopeEntryRootLoop(b *n.Builder) {
	scopeEntryLoopBody(b, false)
}

func scopeEntryLoopBody(b *n.Builder, scoped bool) {
	x := b.ParamNative()
	acc, i := b.Var(b.ConstNative(0)), b.Var(x)
	if scoped {
		b.Enter()
	}
	loop, done := b.NewLabel(), b.NewLabel()
	b.Bind(loop)
	b.Branch(b.IsZero(b.Get(i)), done)
	b.Set(acc, b.Add(b.Get(acc), b.Get(i)))
	b.Set(i, b.Sub(b.Get(i), b.ConstNative(1)))
	b.Jump(loop)
	b.Bind(done)
	result := b.Get(acc)
	if scoped {
		b.Leave(result)
	}
	b.Return(result)
}

func scopeEntryNested(b *n.Builder) {
	scopeEntryNestedBody(b, true)
}

func scopeEntryNestedOnly(b *n.Builder) {
	scopeEntryNestedBody(b, false)
}

func scopeEntryNestedBody(b *n.Builder, outerBranch bool) {
	x := b.ParamNative()
	b.Enter()
	y := b.Add(x, x)
	v := b.Var(y)
	b.Enter()
	// Both scope entries must split when their bodies later gain edges.
	z := b.Add(x, y)
	w := b.Var(z)
	done := b.NewLabel()
	b.Branch(b.IsZero(x), done)
	b.Set(w, b.Add(b.Get(w), x))
	b.Jump(done)
	b.Bind(done)
	result := b.Get(w)
	b.Leave(result)
	b.Set(v, result)
	if outerBranch {
		outerDone := b.NewLabel()
		b.Jump(outerDone)
		b.Bind(outerDone)
	}
	result = b.Get(v)
	b.Leave(result)
	b.Return(result)
}

func scopeEntrySibling(b *n.Builder) {
	x := b.ParamNative()
	b.Enter()
	y := b.Add(x, x)
	b.Leave(y)
	// The first scope has no branch; x must remain usable in this scope.
	b.Enter()
	z := b.Add(x, y)
	v := b.Var(z)
	done := b.NewLabel()
	b.Branch(b.IsZero(x), done)
	b.Set(v, b.Add(b.Get(v), y))
	b.Jump(done)
	b.Bind(done)
	result := b.Get(v)
	b.Leave(result)
	b.Return(result)
}
