//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64)

package nyanjit_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// boundKernels are generators written once and run against unbound and
// bound Builders of both tiers.
var boundKernels = []struct {
	name  string
	build func(*nyanjit.Builder)
	args  [][]uint64
}{
	{"chain", func(b *nyanjit.Builder) {
		x := b.Param()
		for i := 0; i < 40; i++ {
			x = b.Xor(b.Mul(x, b.Const(3)), b.ShiftRight(x, 5))
			x = b.Add(x, b.Const(uint64(i)))
		}
		b.Return(x)
	}, [][]uint64{{12345}, {0}}},
	{"loop", func(b *nyanjit.Builder) {
		n := b.Param()
		i, acc := b.Var(b.Const(0)), b.Var(b.Const(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		x := b.Get(i)
		b.Set(acc, b.Add(b.Get(acc), b.Xor(b.Mul(x, x), b.ShiftRight(x, 3))))
		b.Set(i, b.Add(x, b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(acc))
	}, [][]uint64{{0}, {1}, {1000}}},
	{"collatz", func(b *nyanjit.Builder) {
		n := b.Param()
		x, y, total := b.Var(b.Const(1)), b.Var(b.Const(0)), b.Var(b.Const(0))
		outer, inner, odd, next, done := b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Bind(outer)
		b.Branch(b.Compare(b.Get(x), ir.BranchAbove, n), done)
		b.Set(y, b.Get(x))
		b.Bind(inner)
		b.Branch(b.CompareImmediate(b.Get(y), ir.BranchEqual, 1), next)
		b.Set(total, b.Add(b.Get(total), b.Const(1)))
		b.Branch(b.IsZero(b.And(b.Get(y), b.Const(1))).Not(), odd)
		b.Set(y, b.ShiftRight(b.Get(y), 1))
		b.Jump(inner)
		b.Bind(odd)
		b.Set(y, b.Add(b.Mul(b.Get(y), b.Const(3)), b.Const(1)))
		b.Jump(inner)
		b.Bind(next)
		b.Set(x, b.Add(b.Get(x), b.Const(1)))
		b.Jump(outer)
		b.Bind(done)
		b.Return(b.Get(total))
	}, [][]uint64{{1}, {27}, {1000}}},
	{"scopes-and-floats", func(b *nyanjit.Builder) {
		n := b.Param()
		scale := b.Float64(0.5)
		b.Enter()
		i, sum := b.Var(b.Const(0)), b.Var(b.Float64(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		x := b.FMul(b.FFromSigned(b.Get(i), 64), scale)
		b.Set(sum, b.FAdd(b.Get(sum), x))
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		total := b.Get(sum)
		b.Leave(total)
		b.Return(b.FToSigned(total, 64))
	}, [][]uint64{{0}, {10}, {101}}},
}

func callUnit(t *testing.T, u *compile.Unit, args []uint64) uint64 {
	t.Helper()
	tmpl, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	code, err := tmpl.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	words := make([]uintptr, len(args))
	for i, a := range args {
		words[i] = uintptr(a)
	}
	r, err := code.Call(words...)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(r)
}

func TestBoundBuilderTiersMatchFinish(t *testing.T) {
	f := compile.Func{ABI: abi.C}
	for _, k := range boundKernels {
		b := nyanjit.NewBuilder()
		k.build(b)
		program, err := b.Finish()
		if err != nil {
			t.Fatalf("%s: Finish: %v", k.name, err)
		}
		optimized, err := compile.NewTarget(compile.TargetSpec{})
		if err != nil {
			t.Fatal(err)
		}
		reference, err := compile.Lower(optimized, program, f)
		if err != nil {
			t.Fatalf("%s: Lower: %v", k.name, err)
		}
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
			if errors.Is(err, compile.ErrUnsupported) {
				t.Logf("%s: tier %d unsupported on this host", k.name, tier)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			bound := nyanjit.NewBuilderFor(target, f)
			k.build(bound)
			unit, err := bound.Lower()
			if err != nil {
				t.Fatalf("%s tier %d: Lower: %v", k.name, tier, err)
			}
			if tier == compile.TierOptimized {
				want, _ := reference.Listing()
				got, _ := unit.Listing()
				if got != want {
					t.Errorf("%s: bound optimized listing differs from Finish+Lower:\n%s\nwant:\n%s", k.name, got, want)
				}
			}
			for _, args := range k.args {
				if got, want := callUnit(t, unit, args), callUnit(t, reference, args); got != want {
					t.Errorf("%s tier %d %v: got %d, want %d", k.name, tier, args, got, want)
				}
			}
		}
	}
}

func TestBoundBuilderMisuse(t *testing.T) {
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		t.Fatal(err)
	}
	bound := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.C})
	bound.Return(bound.Param())
	if _, err := bound.Finish(); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Errorf("Finish on a bound Builder: %v", err)
	}
	if _, err := bound.Lower(); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Errorf("Lower after Finish: %v", err)
	}
	unbound := nyanjit.NewBuilder()
	unbound.Return(unbound.Param())
	if _, err := unbound.Lower(); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Errorf("Lower on an unbound Builder: %v", err)
	}
	again := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.C})
	again.Return(again.Param())
	if _, err := again.Lower(); err != nil {
		t.Fatal(err)
	}
	if _, err := again.Lower(); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Errorf("second Lower: %v", err)
	}
	open := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.C})
	l := open.NewLabel()
	open.Jump(l)
	if _, err := open.Lower(); !errors.Is(err, compile.ErrInvalidFunction) {
		t.Errorf("unbound label: %v", err)
	}
}

func TestBoundBuilderRecordsStreamError(t *testing.T) {
	baseline, err := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
	if errors.Is(err, compile.ErrUnsupported) {
		t.Skip("no baseline tier on this host")
	}
	if err != nil {
		t.Fatal(err)
	}
	b := nyanjit.NewBuilderFor(baseline, compile.Func{ABI: abi.C, Harden: true})
	b.Return(b.Add(b.Param(), b.Const(1)))
	if _, err := b.Lower(); !errors.Is(err, compile.ErrUnsupported) {
		t.Errorf("hardened baseline: %v, want ErrUnsupported", err)
	}
}
