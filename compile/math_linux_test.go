//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package compile_test

import (
	"math"
	"math/rand"
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

func floatWord(x float64, width uint8) uint64 {
	if width == 32 {
		return uint64(math.Float32bits(float32(x)))
	}
	return math.Float64bits(x)
}
func wordFloat(x uint64, width uint8) float64 {
	if width == 32 {
		return float64(math.Float32frombits(uint32(x)))
	}
	return math.Float64frombits(x)
}

// Normalize extreme inputs in the test oracle. Some optimized Go assembly
// paths differ from its portable algorithms for subnormal logarithms and
// exponentials close to overflow. These identities give an independent check.
func unaryMathReference(name string, x float64, reference func(float64) float64) float64 {
	if x > 0 && x < 0x1p-1022 {
		if name == "Log" {
			return math.Log(x*0x1p52) - 52*math.Ln2
		}
		if name == "Log10" {
			return math.Log10(x*0x1p52) - 52*math.Log10(2)
		}
	}
	if x > 700 && x <= 709.78 {
		half := math.Exp(x / 2)
		switch name {
		case "Exp":
			return half * half
		case "Sinh", "Cosh":
			return (half * 0.5) * half
		}
	}
	return reference(x)
}
func orderedFloat(x uint64, width uint8) uint64 {
	sign := uint64(1) << (width - 1)
	if x&sign != 0 {
		return ^x & (2*sign - 1)
	}
	return x | sign
}
func mathWordsClose(got, want uint64, width uint8) bool {
	x, y := wordFloat(got, width), wordFloat(want, width)
	if math.IsNaN(y) {
		return math.IsNaN(x)
	}
	if math.IsInf(x, 0) || math.IsInf(y, 0) {
		return got == want
	}
	// Go's assembly and portable implementations can round differently at
	// underflow (for example Exp(-745) on arm64). Compare representable steps.
	if math.Signbit(x) != math.Signbit(y) {
		return false
	}
	a, b := orderedFloat(got, width), orderedFloat(want, width)
	if a > b {
		a, b = b, a
	}
	return b-a <= 16
}

func TestGeneratedUnaryMath(t *testing.T) {
	tests := []struct {
		name      string
		build     func(*nyanjit.Builder, nyanjit.Value) nyanjit.Value
		reference func(float64) float64
	}{
		{"Sin", (*nyanjit.Builder).FSin, math.Sin}, {"Cos", (*nyanjit.Builder).FCos, math.Cos}, {"Tan", (*nyanjit.Builder).FTan, math.Tan},
		{"Asin", (*nyanjit.Builder).FAsin, math.Asin}, {"Acos", (*nyanjit.Builder).FAcos, math.Acos}, {"Atan", (*nyanjit.Builder).FAtan, math.Atan},
		{"Sinh", (*nyanjit.Builder).FSinh, math.Sinh}, {"Cosh", (*nyanjit.Builder).FCosh, math.Cosh}, {"Tanh", (*nyanjit.Builder).FTanh, math.Tanh},
		{"Asinh", (*nyanjit.Builder).FAsinh, math.Asinh}, {"Acosh", (*nyanjit.Builder).FAcosh, math.Acosh}, {"Atanh", (*nyanjit.Builder).FAtanh, math.Atanh},
		{"Exp", (*nyanjit.Builder).FExp, math.Exp}, {"Exp2", (*nyanjit.Builder).FExp2, math.Exp2}, {"Expm1", (*nyanjit.Builder).FExpm1, math.Expm1},
		{"Log", (*nyanjit.Builder).FLog, math.Log}, {"Log2", (*nyanjit.Builder).FLog2, math.Log2}, {"Log10", (*nyanjit.Builder).FLog10, math.Log10}, {"Log1p", (*nyanjit.Builder).FLog1p, math.Log1p},
		{"Cbrt", (*nyanjit.Builder).FCbrt, math.Cbrt}, {"Floor", (*nyanjit.Builder).FFloor, math.Floor}, {"Ceil", (*nyanjit.Builder).FCeil, math.Ceil},
		{"Trunc", (*nyanjit.Builder).FTrunc, math.Trunc}, {"Round", (*nyanjit.Builder).FRound, math.Round}, {"RoundToEven", (*nyanjit.Builder).FRoundToEven, math.RoundToEven},
		{"Sqrt", (*nyanjit.Builder).FSqrt, math.Sqrt},
	}
	inputs := []float64{0, math.Copysign(0, -1), 0.125, -0.125, 0.5, -0.5, 0.7, -0.7, 1, -1, 2, -2, 21, -21, math.Pi / 2, math.Pi, 1 << 29, (1 << 29) - 1, 1e20, 1e100, math.MaxFloat64, math.SmallestNonzeroFloat64, 0x1p-1022, 0x1p-54, 709.78, -745, math.Inf(1), math.Inf(-1), math.NaN()}
	random := rand.New(rand.NewSource(42))
	for i := 0; i < 128; i++ {
		inputs = append(inputs, math.Float64frombits(random.Uint64()))
	}
	for _, test := range tests {
		for _, width := range []uint8{32, 64} {
			t.Run(test.name+strconv.Itoa(int(width)), func(t *testing.T) {
				b := nyanjit.NewBuilder()
				b.Return(test.build(b, b.ParamBits(width)))
				program, err := testFinishRaw(b)
				if err != nil {
					t.Fatal(err)
				}
				code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
				if err != nil {
					t.Fatal(err)
				}
				defer code.Close()
				for _, input := range inputs {
					bits := floatWord(input, width)
					x := wordFloat(bits, width)
					want := floatWord(unaryMathReference(test.name, x, test.reference), width)
					got, err := callMathBits(code, width, bits)
					if err != nil {
						t.Fatal(err)
					}
					if !mathWordsClose(uint64(got), want, width) {
						t.Errorf("%s(%g) = %.17g (%#x), want %.17g (%#x)", test.name, x, wordFloat(uint64(got), width), got, wordFloat(want, width), want)
					}
					if x == 0 && wordFloat(want, width) == 0 && uint64(got) != want {
						t.Errorf("%s must preserve the reference signed zero", test.name)
					}
					if width == 32 && got>>32 != 0 {
						t.Errorf("binary32 result is not zero extended: %#x", got)
					}
				}
			})
		}
	}
}

func TestGeneratedBinaryMath(t *testing.T) {
	tests := []struct {
		name      string
		build     func(*nyanjit.Builder, nyanjit.Value, nyanjit.Value) nyanjit.Value
		reference func(float64, float64) float64
	}{
		{"Pow", (*nyanjit.Builder).FPow, math.Pow}, {"Atan2", (*nyanjit.Builder).FAtan2, math.Atan2}, {"Hypot", (*nyanjit.Builder).FHypot, math.Hypot},
	}
	inputs := []float64{0, math.Copysign(0, -1), 0.5, -0.5, 1, -1, 2, -2, 3, -3, 0x1p53, math.MaxFloat64, math.SmallestNonzeroFloat64, math.Inf(1), math.Inf(-1), math.NaN()}
	for _, test := range tests {
		for _, width := range []uint8{32, 64} {
			t.Run(test.name+strconv.Itoa(int(width)), func(t *testing.T) {
				b := nyanjit.NewBuilder()
				x, y := b.ParamBits(width), b.ParamBits(width)
				b.Return(test.build(b, x, y))
				program, err := testFinishRaw(b)
				if err != nil {
					t.Fatal(err)
				}
				code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
				if err != nil {
					t.Fatal(err)
				}
				defer code.Close()
				for _, x := range inputs {
					for _, y := range inputs {
						xb, yb := floatWord(x, width), floatWord(y, width)
						x, y := wordFloat(xb, width), wordFloat(yb, width)
						want := floatWord(test.reference(x, y), width)
						got, err := callMathBits(code, width, xb, yb)
						if err != nil {
							t.Fatal(err)
						}
						if !mathWordsClose(uint64(got), want, width) {
							t.Errorf("%s(%g,%g) = %.17g (%#x), want %.17g (%#x)", test.name, x, y, wordFloat(uint64(got), width), got, wordFloat(want, width), want)
						}
					}
				}
			})
		}
	}
}

func TestGeneratedMathExpressionsPreserveLiveValues(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.ParamBits(64)
	b.Return(x.FSin().FMul(x.FCos()).FAdd(x.FExp().FLog()))
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	first, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	for _, x := range []float64{-2, -0.125, 0, 0.5, 1, 2} {
		got, err := callMathBits(second, 64, math.Float64bits(x))
		if err != nil {
			t.Fatal(err)
		}
		want := math.Sin(x)*math.Cos(x) + math.Log(math.Exp(x))
		if math.Abs(math.Float64frombits(uint64(got))-want) > 1e-14 {
			t.Fatalf("expression(%g) = %g, want %g", x, math.Float64frombits(uint64(got)), want)
		}
	}
}

func TestGeneratedMathWithUserBindings(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Return(b.FCos(b.Symbol("input")))
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	bindings := map[string]uint64{"input": math.Float64bits(math.Pi)}
	if _, err := lowerListing(program, compile.TargetSpec{}, compile.Func{ABI: abi.C, Constants: bindings}); err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	code, err := template.Bind(bindings)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := callMathBits(code, 64)
	if err != nil || math.Float64frombits(uint64(got)) != -1 {
		t.Fatalf("cos(pi) = %g, %v", math.Float64frombits(uint64(got)), err)
	}
	if len(bindings) != 1 {
		t.Fatal("binding mutated the caller's map")
	}
}

func callMathBits(code *execution.Code, width uint8, values ...uint64) (uint64, error) {
	var args []any
	for _, v := range values {
		if width == 64 {
			args = append(args, v)
		} else {
			args = append(args, uintptr(v))
		}
	}
	lo, hi, err := code.CallPair(execution.Words(args...)...)
	if width == 32 {
		return uint64(lo), err
	}
	return execution.Wide(lo, hi), err
}
