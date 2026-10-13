package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func chainProgram(n int) func(*nyanjit.Builder) {
	return func(b *nyanjit.Builder) {
		x := b.Param()
		for i := 0; i < n; i++ {
			x = b.Mul(x, b.Const(3))
			x = b.Xor(x, b.ShiftRight(x, 5))
			x = b.Add(x, b.Const(uint64(i)))
		}
		b.Return(x)
	}
}

// BenchmarkBaselineLowerChain1000 measures Lower alone at TierBaseline,
// per source operation (4000 for chain1000).
func BenchmarkBaselineLowerChain1000(b *testing.B) {
	bl := nyanjit.NewBuilder()
	chainProgram(1000)(bl)
	p, err := bl.Finish()
	if err != nil {
		b.Fatal(err)
	}
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "arm64", Tier: compile.TierBaseline})
	if err != nil {
		b.Fatal(err)
	}
	f := compile.Func{ABI: abi.C}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := compile.Lower(target, p, f); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/4000, "ns/op-src")
}

// BenchmarkBaselineLowerTiny measures the fixed cost of a baseline Lower.
func BenchmarkBaselineLowerTiny(b *testing.B) {
	bl := nyanjit.NewBuilder()
	bl.Return(bl.Add(bl.Param(), bl.Param()))
	p, err := bl.Finish()
	if err != nil {
		b.Fatal(err)
	}
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: "arm64", Tier: compile.TierBaseline})
	if err != nil {
		b.Fatal(err)
	}
	f := compile.Func{ABI: abi.C}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := compile.Lower(target, p, f); err != nil {
			b.Fatal(err)
		}
	}
}
