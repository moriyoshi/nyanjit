//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le)

package nyanjit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestBindParamsFailureKeepsRequestedLength(t *testing.T) {
	b := nyanjit.NewBuilder()
	label := b.NewLabel()
	values := b.BindParams(label, 2) // Entry has no incoming edge.
	if len(values) != 2 || b.Valid(values[0]) || b.Valid(values[1]) {
		t.Fatalf("BindParams failure returned %#v", values)
	}
	if _, err := b.Finish(); !errors.Is(err, nyanjit.ErrInvalidFunction) {
		t.Fatalf("Finish error = %v", err)
	}
	b = nyanjit.NewBuilder()
	label = b.NewLabel()
	values = b.BindParamsBits(label, []uint8{8, 32})
	if len(values) != 2 || b.Valid(values[0]) {
		t.Fatalf("BindParamsBits failure returned %#v", values)
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
		{"cross-block value", "earlier block", func(b *nyanjit.Builder) {
			value := b.Param()
			label := b.NewLabel()
			b.Jump(label)
			b.Bind(label)
			b.Return(value)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := nyanjit.NewBuilder()
			tc.build(b)
			_, err := b.Finish()
			if !errors.Is(err, nyanjit.ErrInvalidFunction) || !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("Finish error = %v, want %q and ErrInvalidFunction", err, tc.detail)
			}
		})
	}
}

func TestLateLabelAndSingleTargetBranch(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	hit := b.NewLabel()
	if hit < 0 {
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
	code, err := compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC})
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

func TestBranchArgumentsCarryValuesAcrossBlocks(t *testing.T) {
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	bigger, other := b.NewLabel(), b.NewLabel()
	b.BranchArgs(b.Compare(x, nyanjit.BranchGreater, y),
		bigger, []nyanjit.Value{x}, other, []nyanjit.Value{y})
	chosen := b.BindParams(bigger, 1)
	b.Return(chosen[0])
	chosen = b.BindParams(other, 1)
	b.Return(chosen[0])
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC})
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

func TestStacklessLoopWithLabelArguments(t *testing.T) {
	b := nyanjit.NewBuilder()
	n := b.Param()
	loop, done := b.NewLabel(), b.NewLabel()
	b.JumpArgs(loop, b.Const(0), n)
	values := b.BindParams(loop, 2)
	acc, i := values[0], values[1]
	b.BranchArgs(b.IsZero(i), done, []nyanjit.Value{acc}, loop,
		[]nyanjit.Value{b.Add(acc, i), b.Sub(i, b.Const(1))})
	result := b.BindParams(done, 1)
	b.Return(result[0])
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC})
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
			code, err := compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC})
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			buffer := make([]byte, 8)
			got, err := code.CallInto(buffer)
			if err != nil || got != 0xef || buffer[0] != 0xef {
				t.Fatalf("CallInto = %#x, %x, %v", got, buffer, err)
			}
		})
	}
}
