//go:build linux && 386

package compile_test

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

// float386Arg returns x at width w for execution.Words.
func float386Arg(w uint8, x float64) any {
	if w == 32 {
		return float32(x)
	}
	return x
}

func float386Bits(w uint8, x float64) uint64 {
	if w == 32 {
		return uint64(math.Float32bits(float32(x)))
	}
	return math.Float64bits(x)
}

func Test386ScalarFloat(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
				for _, op := range []string{"add", "sub", "mul", "div", "sqrt", "convert", "abs", "neg"} {
					t.Run(fmt.Sprintf("%d/stack%d/abi%d/%s", w, stack, flavor, op), func(t *testing.T) {
						b := nyanjit.NewBuilder()
						x := b.ParamBits(w)
						var y, v nyanjit.Value
						binary := op == "add" || op == "sub" || op == "mul" || op == "div"
						if binary {
							y = b.ParamBits(w)
						}
						resultWidth := w
						switch op {
						case "add":
							v = b.FAdd(x, y)
						case "sub":
							v = b.FSub(x, y)
						case "mul":
							v = b.FMul(x, y)
						case "div":
							v = b.FDiv(x, y)
						case "sqrt":
							v = b.FSqrt(x)
						case "convert":
							resultWidth = 96 - w
							v = b.FConvert(x, resultWidth)
						case "abs":
							v = b.And(x, b.ConstBits(w, (uint64(1)<<(w-1))-1))
						case "neg":
							v = b.Xor(x, b.ConstBits(w, uint64(1)<<(w-1)))
						}
						b.Return(v)
						p, err := testFinishRaw(b)
						if err != nil {
							t.Fatal(err)
						}
						fn := compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: stack}}
						if stack == abi.StackPlatform {
							fn.Stack.BytesAvailable = 8192
						}
						template, err := lowerTemplate(p, compile.TargetSpec{}, fn)
						if err != nil {
							t.Fatal(err)
						}
						report := template.Report()
						arithmetic := op != "abs" && op != "neg"
						if report.FPStatusModified() != arithmetic {
							t.Fatalf("FP status=%t", report.FPStatusModified())
						}
						if arithmetic && !slices.Contains(report.ClobberedFPRegisters(), "XMM3") {
							t.Fatalf("missing allocated XMM3 clobber: %v", report.ClobberedFPRegisters())
						}
						if arithmetic && (w == 64 || resultWidth == 64) && !slices.Contains(report.ClobberedFPRegisters(), "XMM2") {
							t.Fatalf("missing pair-transfer temporary: %v", report.ClobberedFPRegisters())
						}
						wantClobbers := 0
						if arithmetic {
							wantClobbers = 1
							if binary {
								wantClobbers++
							}
							if w == 64 || resultWidth == 64 {
								wantClobbers++
							}
						}
						if len(report.ClobberedFPRegisters()) != wantClobbers {
							t.Fatalf("clobbers=%v want %d registers", report.ClobberedFPRegisters(), wantClobbers)
						}
						code, err := template.Bind(nil)
						if err != nil {
							t.Fatal(err)
						}
						defer code.Close()
						samples := []float64{0, math.Copysign(0, -1), 1, -1, 1.25, -2.5, math.SmallestNonzeroFloat32, math.SmallestNonzeroFloat64, math.MaxFloat32, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN()}
						for i, a := range samples {
							c := samples[(i+3)%len(samples)]
							if w == 32 {
								a, c = float64(float32(a)), float64(float32(c))
							}
							want := a
							switch op {
							case "add":
								want = a + c
							case "sub":
								want = a - c
							case "mul":
								want = a * c
							case "div":
								want = a / c
							case "sqrt":
								want = math.Sqrt(a)
							case "abs":
								want = math.Abs(a)
							case "neg":
								want = -a
							}
							args := execution.Words(float386Arg(w, a))
							if binary {
								args = execution.Words(float386Arg(w, a), float386Arg(w, c))
							}
							lo, hi, err := code.CallPair(args...)
							if err != nil {
								t.Fatal(err)
							}
							got := uint64(lo)
							if resultWidth == 64 {
								got = execution.Wide(lo, hi)
							}
							wantBits := float386Bits(resultWidth, want)
							gotFloat := math.Float64frombits(got)
							if resultWidth == 32 {
								gotFloat = float64(math.Float32frombits(uint32(got)))
							}
							if got != wantBits && !(math.IsNaN(want) && math.IsNaN(gotFloat)) {
								t.Fatalf("a=%g b=%g: got %#x want %#x", a, c, got, wantBits)
							}
						}
					})
				}
			}
		}
	}
}
