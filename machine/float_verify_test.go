package machine

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"
)

func exactFMA32ForTest(a, b, c float32) float32 {
	if math.IsNaN(float64(a)) || math.IsNaN(float64(b)) || math.IsNaN(float64(c)) || math.IsInf(float64(a), 0) || math.IsInf(float64(b), 0) || math.IsInf(float64(c), 0) {
		return float32(math.FMA(float64(a), float64(b), float64(c)))
	}
	af, bf, cf := new(big.Float).SetPrec(512).SetFloat64(float64(a)), new(big.Float).SetPrec(512).SetFloat64(float64(b)), new(big.Float).SetPrec(512).SetFloat64(float64(c))
	sum := new(big.Float).SetPrec(512).Mul(af, bf)
	sum.Add(sum, cf)
	result, _ := sum.Float32()
	return result
}

func TestVerifyFMA32UsesOneRounding(t *testing.T) {
	test := func(a, b, c float32) {
		t.Helper()
		got := uint32(verifyFMA(32, uint64(math.Float32bits(a)), uint64(math.Float32bits(b)), uint64(math.Float32bits(c))))
		want := math.Float32bits(exactFMA32ForTest(a, b, c))
		if math.IsNaN(float64(math.Float32frombits(want))) {
			if !math.IsNaN(float64(math.Float32frombits(got))) {
				t.Fatal("NaN not preserved")
			}
			return
		}
		if got != want {
			t.Fatalf("FMA(%#x,%#x,%#x)=%#x; want %#x", math.Float32bits(a), math.Float32bits(b), math.Float32bits(c), got, want)
		}
	}
	x := float32(1 + math.Ldexp(1, -12))
	for _, sign := range []float32{1, -1} {
		test(sign*x, x, sign*float32(math.Ldexp(1, -80)))
		test(sign*x, x, -sign*float32(math.Ldexp(1, -80)))
	}
	for _, a := range []float32{0, math.Float32frombits(0x80000000), math.SmallestNonzeroFloat32, math.MaxFloat32, float32(math.Inf(1)), float32(math.NaN())} {
		for _, b := range []float32{0, math.Float32frombits(0x80000000), 1, -1, math.SmallestNonzeroFloat32, math.MaxFloat32} {
			for _, c := range []float32{0, math.Float32frombits(0x80000000), math.SmallestNonzeroFloat32, math.MaxFloat32} {
				test(a, b, c)
			}
		}
	}
	random := rand.New(rand.NewPCG(789, 123))
	for i := 0; i < 10000; i++ {
		test(math.Float32frombits(random.Uint32()), math.Float32frombits(random.Uint32()), math.Float32frombits(random.Uint32()))
	}
}
