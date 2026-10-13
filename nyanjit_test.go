//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64)

package nyanjit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Labels belong to their Builder: the zero Label, a Label of another Builder
// and a Label of an earlier, finished Builder are all rejected.
func TestForeignAndStaleLabelsAreRejected(t *testing.T) {
	earlier := nyanjit.NewBuilder()
	stale := earlier.NewLabel()
	earlier.Bind(stale)
	earlier.Return(earlier.Const(0))
	if _, err := earlier.Finish(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*nyanjit.Builder) nyanjit.Label{
		"zero":    func(*nyanjit.Builder) nyanjit.Label { return nyanjit.Label{} },
		"foreign": func(*nyanjit.Builder) nyanjit.Label { return nyanjit.NewBuilder().NewLabel() },
		"stale":   func(*nyanjit.Builder) nyanjit.Label { return stale },
	}
	uses := map[string]func(*nyanjit.Builder, nyanjit.Label){
		"Bind": func(b *nyanjit.Builder, l nyanjit.Label) { b.Bind(l) },
		"Jump": func(b *nyanjit.Builder, l nyanjit.Label) { b.Jump(l) },
		"Branch": func(b *nyanjit.Builder, l nyanjit.Label) {
			b.Branch(b.IsZero(b.Param()), l)
		},
	}
	for name, label := range cases {
		for use, apply := range uses {
			t.Run(name+"/"+use, func(t *testing.T) {
				b := nyanjit.NewBuilder()
				own := b.NewLabel() // Same index as the other Builders' first label.
				apply(b, label(b))
				b.Jump(own)
				b.Bind(own)
				b.Return(b.Const(1))
				if _, err := b.Finish(); !errors.Is(err, compile.ErrInvalidFunction) {
					t.Fatalf("Finish error = %v, want ErrInvalidFunction", err)
				}
			})
		}
	}
}

func TestDeferredErrorsExplainRule(t *testing.T) {
	cases := []struct {
		name, detail string
		build        func(*nyanjit.Builder)
	}{
		{"missing return", "no return", func(b *nyanjit.Builder) { b.Param() }},
		{"foreign value", "another builder", func(b *nyanjit.Builder) {
			foreign := nyanjit.NewBuilder().Param()
			b.Return(b.Add(foreign, b.Const(1)))
		}},
		{"width mismatch", "widths 64 and 32", func(b *nyanjit.Builder) {
			b.Return(b.Add(b.Param(), b.ParamBits(32)))
		}},
		{"store width", "want 32 or 64", func(b *nyanjit.Builder) {
			b.Store32(b.Param(), b.ConstBits(16, 1))
		}},
		{"second return", "already has a return", func(b *nyanjit.Builder) {
			one := b.Const(1)
			b.Return(one)
			b.Return(one)
		}},
		{"unbound label", "unbound", func(b *nyanjit.Builder) {
			label := b.NewLabel()
			b.Jump(label)
		}},
		{"non-dominating value", "does not dominate", func(b *nyanjit.Builder) {
			x := b.Param()
			left, right, join := b.NewLabel(), b.NewLabel(), b.NewLabel()
			b.Branch(b.IsZero(x), left)
			b.Jump(right)
			b.Bind(left)
			y := b.Add(x, b.Const(1))
			b.Jump(join)
			b.Bind(right)
			b.Jump(join)
			b.Bind(join)
			b.Return(y)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := nyanjit.NewBuilder()
			tc.build(b)
			_, err := b.Finish()
			if !errors.Is(err, compile.ErrInvalidFunction) || !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("Finish error = %v, want %q and ErrInvalidFunction", err, tc.detail)
			}
		})
	}
}

func TestLateLabelAndSingleTargetBranch(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	hit := b.NewLabel()
	if hit == (nyanjit.Label{}) {
		t.Fatal("NewLabel after Param returned an invalid label")
	}
	b.Branch(b.IsZero(x), hit)
	b.Return(b.Const(2))
	b.Bind(hit)
	b.Return(b.Const(1))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, tc := range []struct{ input, want uint64 }{{0, 1}, {5, 2}} {
		got, err := code.Call(uintptr(tc.input))
		if err != nil || uint64(got) != tc.want {
			t.Fatalf("Call(%d) = %d, %v; want %d", tc.input, got, err, tc.want)
		}
	}
}

func TestVarCarriesValuesAcrossBlocks(t *testing.T) {
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	chosen := b.Var(y)
	bigger, join := b.NewLabel(), b.NewLabel()
	b.Branch(b.Compare(x, ir.BranchGreater, y), bigger)
	b.Jump(join)
	b.Bind(bigger)
	b.Set(chosen, x)
	b.Bind(join)
	b.Return(b.Get(chosen))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, tc := range []struct{ x, y, want uint64 }{
		{7, 5, 7}, {^uint64(2), 5, 5},
	} {
		got, err := code.Call(uintptr(tc.x), uintptr(tc.y))
		if err != nil || uint64(got) != tc.want {
			t.Fatalf("max(%d, %d) = %d, %v; want %d", int64(tc.x), int64(tc.y), got, err, tc.want)
		}
	}
}

func TestStacklessLoopWithVars(t *testing.T) {
	b := nyanjit.NewBuilder()
	n := b.Param()
	acc, i := b.Var(b.Const(0)), b.Var(n)
	loop, done := b.NewLabel(), b.NewLabel()
	b.Bind(loop)
	b.Branch(b.IsZero(b.Get(i)), done)
	b.Set(acc, b.Add(b.Get(acc), b.Get(i)))
	b.Set(i, b.Sub(b.Get(i), b.Const(1)))
	b.Jump(loop)
	b.Bind(done)
	b.Return(b.Get(acc))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call(100)
	if err != nil || got != 5050 {
		t.Fatalf("sum(100) = %d, %v; want 5050", got, err)
	}
}

func TestMatchingWidthStores(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store func(*nyanjit.Builder, nyanjit.Value)
	}{
		{"Store8", func(b *nyanjit.Builder, p nyanjit.Value) { b.Store8(p, b.ConstBits(8, 0xef)) }},
		{"Store16", func(b *nyanjit.Builder, p nyanjit.Value) { b.Store16(p, b.ConstBits(16, 0xbeef)) }},
		{"Store32", func(b *nyanjit.Builder, p nyanjit.Value) { b.Store32(p, b.ConstBits(32, 0xdeadbeef)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := nyanjit.NewBuilder()
			p := b.Param()
			tc.store(b, p)
			b.Return(b.Load8(p))
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			buffer := make([]byte, 8)
			got, err := code.Call(execution.Words(&buffer[0])...)
			if err != nil || got != 0xef || buffer[0] != 0xef {
				t.Fatalf("Call(Words) = %#x, %x, %v", got, buffer, err)
			}
		})
	}
}
