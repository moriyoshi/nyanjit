package compile_test

import (
	"os"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// baseline386Kernels are small programs the 386 baseline tests lower and
// run against the optimized tier.
var baseline386Kernels = []struct {
	name  string
	build func(*nyanjit.Builder)
	args  [][]uint64
}{
	{"chain", func(b *nyanjit.Builder) {
		x := b.CastUnsigned(b.Param(), 32)
		for i := 0; i < 20; i++ {
			x = b.Mul(x, b.ConstBits(32, 3))
			x = b.Xor(x, b.ShiftRight(x, 5))
			x = b.Add(x, b.ConstBits(32, uint64(i)))
		}
		b.Return(b.CastUnsigned(x, 64))
	}, [][]uint64{{12345}, {0}, {^uint64(0)}}},
	{"chain64", func(b *nyanjit.Builder) {
		x := b.Param()
		for i := 0; i < 20; i++ {
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
	{"collatz", func(b *nyanjit.Builder) {
		n := b.Var(b.Param())
		steps := b.Var(b.Const(0))
		loop, odd, next, done := b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(n), ir.BranchBelowEqual, b.Const(1)), done)
		b.Branch(b.Compare(b.And(b.Get(n), b.Const(1)), ir.BranchNotEqual, b.Const(0)), odd)
		b.Set(n, b.ShiftRight(b.Get(n), 1))
		b.Jump(next)
		b.Bind(odd)
		b.Set(n, b.Add(b.Mul(b.Get(n), b.Const(3)), b.Const(1)))
		b.Bind(next)
		b.Set(steps, b.Add(b.Get(steps), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(steps))
	}, [][]uint64{{1}, {27}, {97}, {1 << 40}}},
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

func lower386(t testing.TB, tier compile.Tier, build func(*nyanjit.Builder), f compile.Func) *compile.Unit {
	t.Helper()
	b := nyanjit.NewBuilder()
	build(b)
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "386", Tier: tier})
	if err != nil {
		t.Fatal(err)
	}
	u, err := compile.Lower(target, p, f)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// TestBaseline386Listings lowers the kernels for 386 at both tiers; with
// BASELINE386_LISTING set it logs the listings.
func TestBaseline386Listings(t *testing.T) {
	for _, f := range []compile.Func{
		{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}},
		{ABI: abi.C},
	} {
		for _, k := range baseline386Kernels {
			u := lower386(t, compile.TierBaseline, k.build, f)
			l, err := u.Listing()
			if err != nil {
				t.Fatalf("%s: listing: %v", k.name, err)
			}
			if os.Getenv("BASELINE386_LISTING") == k.name {
				t.Logf("%s stack=%v report=%+v\n%s", k.name, f.Stack.Practice, u.Report(), l)
			}
		}
	}
}

func chain32(n int) func(*nyanjit.Builder) {
	return func(b *nyanjit.Builder) {
		x := b.CastUnsigned(b.Param(), 32)
		for i := 0; i < n; i++ {
			x = b.Mul(x, b.ConstBits(32, 3))
			x = b.Xor(x, b.ShiftRight(x, 5))
			x = b.Add(x, b.ConstBits(32, uint64(i)))
		}
		b.Return(b.CastUnsigned(x, 64))
	}
}

func benchLower386(b *testing.B, tier compile.Tier, build func(*nyanjit.Builder), ops int) {
	bl := nyanjit.NewBuilder()
	build(bl)
	p, err := bl.Finish()
	if err != nil {
		b.Fatal(err)
	}
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "386", Tier: tier})
	if err != nil {
		b.Fatal(err)
	}
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := compile.Lower(target, p, f); err != nil {
			b.Fatal(err)
		}
	}
	if ops > 0 {
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(ops), "ns/op-src")
	}
}

func BenchmarkBaseline386Chain32(b *testing.B) {
	for _, tier := range []compile.Tier{compile.TierBaseline, compile.TierOptimized} {
		b.Run([]string{"optimized", "baseline"}[tier], func(b *testing.B) { benchLower386(b, tier, chain32(1000), 4000) })
	}
}

func BenchmarkBaseline386Loop(b *testing.B) {
	for _, tier := range []compile.Tier{compile.TierBaseline, compile.TierOptimized} {
		b.Run([]string{"optimized", "baseline"}[tier], func(b *testing.B) { benchLower386(b, tier, baseline386Kernels[2].build, 0) })
	}
}
