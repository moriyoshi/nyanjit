//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"math"
	"math/big"
	"math/rand"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

func ulpDistance(got, want float64) uint64 {
	if math.IsNaN(got) && math.IsNaN(want) {
		return 0
	}
	a, b := orderedFloat(math.Float64bits(got), 64), orderedFloat(math.Float64bits(want), 64)
	if a > b {
		return a - b
	}
	return b - a
}

func compileUnaryMath(t *testing.T, build func(*nyanjit.Builder, nyanjit.Value) nyanjit.Value) *compile.Code {
	t.Helper()
	b := nyanjit.NewBuilder()
	b.Return(build(b, b.ParamBits(64)))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// TestInlineMathRandomInputs compares random inputs with Go's math package.
// Inline Acos and Log2 avoid cancellations that Go's versions have near 1,
// so those inputs are excluded here.
func TestInlineMathRandomInputs(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	generators := []func() float64{
		func() float64 { return math.Float64frombits(random.Uint64()) },
		func() float64 { return (random.Float64()*2 - 1) * math.Ldexp(1, random.Intn(40)-20) },
		func() float64 { return (random.Float64()*2 - 1) * math.Ldexp(1, random.Intn(70)-10) },
		func() float64 { return random.Float64()*2 - 1 },
	}
	tests := []struct {
		name      string
		build     func(*nyanjit.Builder, nyanjit.Value) nyanjit.Value
		reference func(float64) float64
		skip      func(float64) bool
	}{
		{"Sin", (*nyanjit.Builder).FSin, math.Sin, nil}, {"Cos", (*nyanjit.Builder).FCos, math.Cos, nil},
		{"Tan", (*nyanjit.Builder).FTan, math.Tan, nil}, {"Asin", (*nyanjit.Builder).FAsin, math.Asin, nil},
		{"Acos", (*nyanjit.Builder).FAcos, math.Acos, func(x float64) bool { return math.Abs(x) > 0.9 }},
		{"Atan", (*nyanjit.Builder).FAtan, math.Atan, nil}, {"Sinh", (*nyanjit.Builder).FSinh, math.Sinh, nil},
		{"Cosh", (*nyanjit.Builder).FCosh, math.Cosh, nil}, {"Tanh", (*nyanjit.Builder).FTanh, math.Tanh, nil},
		{"Asinh", (*nyanjit.Builder).FAsinh, math.Asinh, nil}, {"Acosh", (*nyanjit.Builder).FAcosh, math.Acosh, nil},
		{"Atanh", (*nyanjit.Builder).FAtanh, math.Atanh, nil}, {"Exp", (*nyanjit.Builder).FExp, math.Exp, nil},
		{"Exp2", (*nyanjit.Builder).FExp2, math.Exp2, nil}, {"Expm1", (*nyanjit.Builder).FExpm1, math.Expm1, nil},
		{"Log", (*nyanjit.Builder).FLog, math.Log, nil},
		{"Log2", (*nyanjit.Builder).FLog2, math.Log2, func(x float64) bool { return math.Abs(x-1) < 0.25 }},
		{"Log10", (*nyanjit.Builder).FLog10, math.Log10, nil}, {"Log1p", (*nyanjit.Builder).FLog1p, math.Log1p, nil},
		{"Cbrt", (*nyanjit.Builder).FCbrt, math.Cbrt, nil}, {"Floor", (*nyanjit.Builder).FFloor, math.Floor, nil},
		{"Ceil", (*nyanjit.Builder).FCeil, math.Ceil, nil}, {"Trunc", (*nyanjit.Builder).FTrunc, math.Trunc, nil},
		{"Round", (*nyanjit.Builder).FRound, math.Round, nil}, {"RoundToEven", (*nyanjit.Builder).FRoundToEven, math.RoundToEven, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code := compileUnaryMath(t, test.build)
			defer code.Close()
			for _, generate := range generators {
				for i := 0; i < 2000; i++ {
					x := generate()
					if test.skip != nil && test.skip(x) {
						continue
					}
					bits, err := code.Call(uintptr(math.Float64bits(x)))
					if err != nil {
						t.Fatal(err)
					}
					got, want := math.Float64frombits(uint64(bits)), test.reference(x)
					if ulpDistance(got, want) > 4 {
						t.Fatalf("%s(%v) = %v, want %v", test.name, x, got, want)
					}
				}
			}
		})
	}
}

// TestInlinePowIntegerExponents checks against exactly rounded powers.
// Go's math.Pow uses repeated squaring and can be off by hundreds of ulps.
func TestInlinePowIntegerExponents(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Return(b.FPow(b.ParamBits(64), b.ParamBits(64)))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.Compile(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	random := rand.New(rand.NewSource(11))
	for i := 0; i < 2000; i++ {
		x := (random.Float64()*2 - 1) * math.Ldexp(1, random.Intn(8)-2)
		n := random.Intn(301) - 150
		exact := new(big.Float).SetPrec(2000).SetInt64(1)
		base := new(big.Float).SetPrec(2000).SetFloat64(x)
		for k := 0; k < abs(n); k++ {
			exact.Mul(exact, base)
		}
		if n < 0 {
			exact.Quo(new(big.Float).SetPrec(2000).SetInt64(1), exact)
		}
		want, _ := exact.Float64()
		bits, err := code.Call(uintptr(math.Float64bits(x)), uintptr(math.Float64bits(float64(n))))
		if err != nil {
			t.Fatal(err)
		}
		if got := math.Float64frombits(uint64(bits)); ulpDistance(got, want) > 1 {
			t.Fatalf("Pow(%v, %d) = %v, want %v", x, n, got, want)
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
