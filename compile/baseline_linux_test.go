//go:build linux && (arm64 || amd64 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

type baselineKernel struct {
	name  string
	build func(*nyanjit.Builder)
	args  [][]uint64
}

var baselineKernels = []baselineKernel{
	{"tiny", func(b *nyanjit.Builder) { b.Return(b.Add(b.Param(), b.Param())) }, [][]uint64{{3, 4}}},
	{"chain", func(b *nyanjit.Builder) {
		x := b.Param()
		for i := 0; i < 50; i++ {
			x = b.Mul(x, b.Const(3))
			x = b.Xor(x, b.ShiftRight(x, 5))
			x = b.Add(x, b.Const(uint64(i)))
		}
		b.Return(x)
	}, [][]uint64{{12345}, {0}, {^uint64(0)}}},
	{"loop-vars", func(b *nyanjit.Builder) {
		n := b.Param()
		i, acc := b.Var(b.Const(0)), b.Var(b.Const(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		x := b.Get(i)
		b.Set(acc, b.Add(b.Get(acc), b.Xor(b.Mul(x, x), b.ShiftRight(x, 3))))
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(acc))
	}, [][]uint64{{0}, {1}, {1000}}},
	{"pressure", func(b *nyanjit.Builder) {
		const vars = 16
		n := b.Param()
		i := b.Var(b.Const(0))
		a := make([]nyanjit.Var, vars)
		for k := range a {
			a[k] = b.Var(b.Const(uint64(k + 1)))
		}
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		for k := range a {
			b.Set(a[k], b.Add(b.Get(a[k]), b.Xor(b.Get(a[(k+1)%vars]), b.Get(i))))
		}
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		x := b.Get(a[0])
		for _, v := range a[1:] {
			x = b.Xor(x, b.Get(v))
		}
		b.Return(x)
	}, [][]uint64{{0}, {3}, {1000}}},
	{"fp", func(b *nyanjit.Builder) {
		n := b.Param()
		i, count := b.Var(b.Const(0)), b.Var(b.Const(0))
		loop, miss, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		x := b.FMul(b.FFromSigned(b.Get(i), 64), b.Float64(0.001))
		y := b.Float64(0.1)
		for _, c := range []float64{-0.2, 0.3, -0.4, 0.5} {
			y = b.FAdd(b.FMul(y, x), b.Float64(c))
		}
		b.Branch(b.FComparePredicate(y, ir.FloatUnorderedLessEqual, b.Float64(0.05)), miss)
		b.Set(count, b.Add(b.Get(count), b.Const(1)))
		b.Bind(miss)
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(count))
	}, [][]uint64{{0}, {100}, {3000}}},
}

func runBoth(t *testing.T, name string, build func(*nyanjit.Builder), f compile.Func, args [][]uint64) {
	t.Helper()
	b := nyanjit.NewBuilder()
	build(b)
	p, err := b.Finish()
	if err != nil {
		t.Fatalf("%s: finish: %v", name, err)
	}
	opt, err := nyanjit.Compile(p, compile.TargetSpec{}, f)
	if err != nil {
		t.Fatalf("%s: optimized: %v", name, err)
	}
	defer opt.Close()
	base, err := nyanjit.Compile(p, compile.TargetSpec{Tier: compile.TierBaseline}, f)
	if err != nil {
		t.Fatalf("%s: baseline: %v", name, err)
	}
	defer base.Close()
	for _, in := range args {
		words := make([]any, len(in))
		for i, x := range in {
			words[i] = x
		}
		want, err := opt.Call(execution.Words(words...)...)
		if err != nil {
			t.Fatal(err)
		}
		got, err := base.Call(execution.Words(words...)...)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			target, _ := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
			unit, _ := compile.Lower(target, p, f)
			listing, _ := unit.Listing()
			t.Errorf("%s%v: baseline %#x, optimized %#x\n%s", name, in, got, want, listing)
		}
	}
}

func TestBaselineKernels(t *testing.T) {
	for _, f := range []compile.Func{
		{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}},
		{ABI: abi.C},
	} {
		for _, k := range baselineKernels {
			runBoth(t, k.name, k.build, f, k.args)
		}
	}
}

func TestBaselineSymbolRelocation(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Return(b.Add(b.Param(), b.Symbol("k")))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	target, _ := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
	u, err := compile.Lower(target, p, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	code, err := tmpl.Bind(execution.Symbols{"k": 0x123456789})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	if got, _ := code.Call(execution.Words(uint64(1))...); got != 0x12345678a {
		t.Fatalf("got %#x", got)
	}
}

// TestBaselineLongBranches runs a loop whose exit branch spans more than the
// 1 MiB reach of a conditional branch.
func TestBaselineLongBranches(t *testing.T) {
	if testing.Short() {
		t.Skip("large function")
	}
	build := func(b *nyanjit.Builder) {
		n := b.Param()
		i, acc := b.Var(b.Const(0)), b.Var(b.Const(1))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		x := b.Get(acc)
		for k := 0; k < 70000; k++ {
			x = b.Xor(b.Mul(x, b.Const(3)), b.ShiftRight(x, 7))
		}
		b.Set(acc, x)
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(acc))
	}
	runBoth(t, "long", build, compile.Func{ABI: abi.C}, [][]uint64{{0}, {1}, {3}})
}

// TestBaselineSpillLimit checks that a function needing more spill words
// than the frame holds returns a LimitError.
func TestBaselineSpillLimit(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	var vs []nyanjit.Value
	for k := 0; k < 5000; k++ {
		vs = append(vs, b.Add(x, b.Const(uint64(k))))
	}
	s := vs[0]
	for _, v := range vs[1:] {
		s = b.Xor(s, v)
	}
	b.Return(s)
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	target, _ := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
	_, err = compile.Lower(target, p, compile.Func{ABI: abi.C})
	var limit *compile.LimitError
	if !errors.As(err, &limit) || limit.Kind != compile.LimitSpillWords {
		t.Fatalf("got %v", err)
	}
	_, err = compile.Lower(target, p, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 64}})
	if !errors.As(err, &limit) && !errors.Is(err, compile.ErrStackRequired) {
		t.Fatalf("small frame: %v", err)
	}
}
