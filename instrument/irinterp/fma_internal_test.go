package irinterp

import (
	"math"
	"math/big"
	"math/rand"
	"testing"
)

// fma32 rounds p*q+r once, matching an exact rational sum.
func TestFMA32RoundsOnce(t *testing.T) {
	// a*b = 2^-24(1-2^-46); with c = 1+2^-23 the exact sum lies just below
	// a binary32 halfway point, which binary64 rounds onto.
	a, b, c := float32(1+0x1p-23), float32(0x1p-24*(1-0x1p-23)), float32(1+0x1p-23)
	if float32(math.FMA(float64(a), float64(b), float64(c))) == fma32Reference(a, b, c) {
		t.Fatal("case does not distinguish double rounding")
	}
	cases := [][3]float32{{a, b, c}, {-a, b, -c}}
	r := rand.New(rand.NewSource(1))
	word := func() float32 {
		// Uniform bit patterns reach subnormals; small exponent spreads make
		// cancellation and halfway sums likely.
		if r.Intn(2) == 0 {
			return math.Float32frombits(r.Uint32())
		}
		return float32(math.Ldexp(float64(1+r.Int31n(1<<24))/(1<<24), r.Intn(60)-30)) * float32(1-2*r.Intn(2))
	}
	for range 200000 {
		cases = append(cases, [3]float32{word(), word(), word()})
	}
	doubled := 0
	for _, x := range cases {
		got, want := fma32(x[0], x[1], x[2]), fma32Reference(x[0], x[1], x[2])
		if math.Float32bits(got) != math.Float32bits(want) && !(got != got && want != want) {
			t.Fatalf("fma32(%#x, %#x, %#x) = %#x, want %#x", math.Float32bits(x[0]), math.Float32bits(x[1]), math.Float32bits(x[2]), math.Float32bits(got), math.Float32bits(want))
		}
		if float32(math.FMA(float64(x[0]), float64(x[1]), float64(x[2]))) != want {
			doubled++
		}
	}
	t.Logf("%d of %d cases double round through binary64", doubled, len(cases))
}

// fma32Reference rounds the exact rational p*q+r once to binary32; zero,
// infinite and NaN operands follow IEEE 754 through binary64, where they are
// exact.
func fma32Reference(p, q, r float32) float32 {
	x, y, z := float64(p), float64(q), float64(r)
	if x == 0 || y == 0 || z == 0 || math.IsInf(x, 0) || math.IsInf(y, 0) || math.IsInf(z, 0) || x != x || y != y || z != z {
		return float32(math.FMA(x, y, z))
	}
	sum := new(big.Rat).Mul(new(big.Rat).SetFloat64(x), new(big.Rat).SetFloat64(y))
	sum.Add(sum, new(big.Rat).SetFloat64(z))
	if sum.Sign() == 0 {
		return 0
	}
	f, _ := sum.Float32()
	return f
}
