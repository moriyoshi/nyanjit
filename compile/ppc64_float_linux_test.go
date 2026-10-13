//go:build linux && ppc64le

package compile_test

import (
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

func ppcFloatProgram(t *testing.T, width uint8, operation string, expression bool) (*nyanjit.Builder, func(uint64, uint64, uint64) uint64) {
	t.Helper()
	b := nyanjit.NewBuilder()
	x, y := b.ParamBits(width), b.ParamBits(width)
	var z, result nyanjit.Value
	if operation == "fma" {
		z = b.ParamBits(width)
	}
	if expression {
		if operation != "fma" {
			t.Fatal("expression test requires FMA")
		}
		result = x.FMul(y).FAdd(z)
	} else {
		switch operation {
		case "add":
			result = b.FAdd(x, y)
		case "sub":
			result = b.FSub(x, y)
		case "mul":
			result = b.FMul(x, y)
		case "div":
			result = b.FDiv(x, y)
		case "fma":
			result = b.FMA(x, y, z)
		default:
			t.Fatalf("unknown operation %s", operation)
		}
	}
	b.Return(result)
	reference := func(x, y, z uint64) uint64 {
		if width == 64 {
			a, b, c := math.Float64frombits(x), math.Float64frombits(y), math.Float64frombits(z)
			var result float64
			switch operation {
			case "add":
				result = a + b
			case "sub":
				result = a - b
			case "mul":
				result = a * b
			case "div":
				result = a / b
			case "fma":
				result = math.FMA(a, b, c)
			}
			return math.Float64bits(result)
		}
		a, b, c := math.Float32frombits(uint32(x)), math.Float32frombits(uint32(y)), math.Float32frombits(uint32(z))
		var result float32
		switch operation {
		case "add":
			result = a + b
		case "sub":
			result = a - b
		case "mul":
			result = a * b
		case "div":
			result = a / b
		case "fma":
			if math.IsNaN(float64(a)) || math.IsNaN(float64(b)) || math.IsNaN(float64(c)) || math.IsInf(float64(a), 0) || math.IsInf(float64(b), 0) || math.IsInf(float64(c), 0) {
				result = float32(math.FMA(float64(a), float64(b), float64(c)))
			} else {
				// Binary32 FMA must round directly to binary32. FMA64 followed by
				// a float32 cast can double round on halfway cases. 512 bits retain
				// the exact product and sum over the entire binary32 exponent range.
				af := new(big.Float).SetPrec(512).SetFloat64(float64(a))
				bf := new(big.Float).SetPrec(512).SetFloat64(float64(b))
				cf := new(big.Float).SetPrec(512).SetFloat64(float64(c))
				exact := new(big.Float).SetPrec(512).Mul(af, bf)
				exact.Add(exact, cf)
				result, _ = exact.Float32()
			}
		}
		return uint64(math.Float32bits(result))
	}
	return b, reference
}

func ppcFloatEqual(width uint8, got, want uint64) bool {
	if width == 32 {
		if got>>32 != 0 {
			return false
		}
		if math.IsNaN(float64(math.Float32frombits(uint32(want)))) {
			return math.IsNaN(float64(math.Float32frombits(uint32(got))))
		}
	} else if math.IsNaN(math.Float64frombits(want)) {
		return math.IsNaN(math.Float64frombits(got))
	}
	return got == want
}

func TestPPC64FloatingArithmeticAndFMA(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		values := []uint64{0, 0x8000000000000000, math.Float64bits(1), math.Float64bits(-3.5), math.Float64bits(1.0 / 3), math.Float64bits(1e100), math.Float64bits(1e-100), 1, 0x8000000000000001, 0x7fefffffffffffff, 0x0010000000000000, 0x000fffffffffffff, 0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000001, 0x7ff0000000000001}
		if width == 32 {
			values = []uint64{0, 0x80000000, uint64(math.Float32bits(1)), uint64(math.Float32bits(-3.5)), uint64(math.Float32bits(1.0 / 3)), uint64(math.Float32bits(1e30)), uint64(math.Float32bits(1e-30)), 1, 0x80000001, 0x7f7fffff, 0x00800000, 0x007fffff, 0x7f800000, 0xff800000, 0x7fc00001, 0x7f800001}
		}
		for _, operation := range []string{"add", "sub", "mul", "div", "fma"} {
			t.Run(fmt.Sprintf("%d/%s", width, operation), func(t *testing.T) {
				b, reference := ppcFloatProgram(t, width, operation, false)
				code, report := compilePPC(t, b, compile.Func{ABI: abi.C})
				if report.StackBytes != 0 || report.WorkspaceBytes != 0 || report.FlagsClobbered {
					t.Fatalf("FP leaf uses stack/workspace or changes integer flags: %+v", report)
				}
				if !report.FPStatusModified || !slices.Contains(report.ClobberedSpecialRegisters, "FPSCR") {
					t.Fatalf("missing FPSCR modification: %+v", report)
				}
				expected := []string{"F3", "F4"}
				if operation == "fma" {
					expected = append(expected, "F5")
				}
				if width == 32 {
					expected = append(expected, "F0")
				} // non-signaling bit-pattern extraction
				if !slices.Equal(report.ClobberedFPRegisters, expected) {
					t.Fatalf("FP clobbers=%v; want %v", report.ClobberedFPRegisters, expected)
				}
				for _, name := range []string{"R0", "R1", "R2", "R11", "R12", "R13", "R14", "R29", "R30", "R31"} {
					if slices.Contains(report.ClobberedRegisters, name) {
						t.Fatalf("unexpected clobber %s", name)
					}
				}
				invoke := func(x, y, z uint64) uint64 {
					args := []any{uintptr(x), uintptr(y)}
					if operation == "fma" {
						args = append(args, uintptr(z))
					}
					got, err := code.Call(execution.Words(args...)...)
					if err != nil {
						t.Fatal(err)
					}
					return uint64(got)
				}
				for _, x := range values {
					for _, y := range values {
						z := values[(int(x^y)%len(values)+len(values))%len(values)] // Stable variety including signed zeros and NaNs.
						want := reference(x, y, z)
						got := invoke(x, y, z)
						if !ppcFloatEqual(width, got, want) {
							t.Fatalf("%s(%#x,%#x,%#x)=%#x; want %#x", operation, x, y, z, got, want)
						}
					}
				}
				random := rand.New(rand.NewPCG(123, 456))
				for i := 0; i < 300; i++ {
					x, y, z := random.Uint64(), random.Uint64(), random.Uint64()
					if width == 32 {
						x, y, z = uint64(uint32(x)), uint64(uint32(y)), uint64(uint32(z))
					}
					got, want := invoke(x, y, z), reference(x, y, z)
					if !ppcFloatEqual(width, got, want) {
						t.Fatalf("random %s(%#x,%#x,%#x)=%#x; want %#x", operation, x, y, z, got, want)
					}
				}
			})
		}
	}
}

func TestPPC64DeferredMultiplyAddContractsWithOneRounding(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		for _, form := range []string{"explicit", "multiply first", "multiply second", "staged"} {
			t.Run(fmt.Sprintf("%d/%s", width, form), func(t *testing.T) {
				b := nyanjit.NewBuilder()
				x, y, z := b.ParamBits(width), b.ParamBits(width), b.ParamBits(width)
				var result nyanjit.Value
				switch form {
				case "explicit":
					result = b.FMA(x, y, z)
				case "multiply first":
					result = x.FMul(y).FAdd(z)
				case "multiply second":
					result = z.FAdd(x.FMul(y))
				case "staged":
					result = b.FAdd(b.FMul(x, y), z)
				}
				b.Return(result)
				program := finishPPC(t, b)
				assembly, err := lowerListing(program, compile.TargetSpec{}, compile.Func{})
				if err != nil {
					t.Fatal(err)
				}
				fused := form != "staged"
				name := "fmadd"
				if width == 32 {
					name = "fmadds"
				}
				if strings.Contains(assembly, name) != fused {
					t.Fatalf("unexpected contraction:\n%s", assembly)
				}
				code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{})
				if err != nil {
					t.Fatal(err)
				}
				defer code.Close()
				xbits, ybits, zbits := math.Float64bits(1+math.Ldexp(1, -27)), math.Float64bits(1-math.Ldexp(1, -27)), math.Float64bits(-1)
				want := math.Float64bits(-math.Ldexp(1, -54))
				if width == 32 {
					xbits = uint64(math.Float32bits(1 + float32(math.Ldexp(1, -13))))
					ybits = uint64(math.Float32bits(1 - float32(math.Ldexp(1, -13))))
					zbits = uint64(math.Float32bits(-1))
					want = uint64(math.Float32bits(-float32(math.Ldexp(1, -26))))
				}
				if !fused {
					want = 0
				}
				got, err := code.Call(execution.Words(uintptr(xbits), uintptr(ybits), uintptr(zbits))...)
				if err != nil || uint64(got) != want {
					t.Fatalf("rounding=%#x,%v; want %#x", got, err, want)
				}
			})
		}
	}
}

//go:noinline
func ppcFloatGC(value uint64) uint64 { runtime.GC(); return value }

func TestPPC64FloatingValuesSurviveCallsAndScopes(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			b := nyanjit.NewBuilder()
			x, y := b.ParamBits(width), b.ParamBits(width)
			live := b.FMul(x, y)
			b.Enter()
			called := b.CallGo(b.Const(uint64(reflect.ValueOf(ppcFloatGC).Pointer())), b.Const(42))
			b.Leave(called)
			// Keep the Go call observable and reuse the pre-call FP bit pattern.
			b.Return(b.Xor(b.FAdd(live, x), b.CastUnsigned(called, width)))
			program := finishPPC(t, b)
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: flavor, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}, AssumePointerFree: true})
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			xbits, ybits := math.Float64bits(2), math.Float64bits(3)
			want := math.Float64bits(8) ^ 42
			if width == 32 {
				xbits, ybits = uint64(math.Float32bits(2)), uint64(math.Float32bits(3))
				want = uint64(math.Float32bits(8)) ^ 42
			}
			got, err := code.Call(execution.Words(uintptr(xbits), uintptr(ybits))...)
			if err != nil || uint64(got) != want {
				t.Fatalf("width %d entry %d = %#x,%v; want %#x", width, flavor, got, err, want)
			}
		}
	}
}

// With this input the exact product is a binary32 midpoint. A tiny positive
// addend puts the exact sum above it, but FMA64 loses that addend and a later
// binary32 conversion would round to the opposite neighbor.
func TestPPC64SingleFMAAvoidsDoubleRounding(t *testing.T) {
	b, _ := ppcFloatProgram(t, 32, "fma", false)
	code, _ := compilePPC(t, b, compile.Func{})
	x := float32(1 + math.Ldexp(1, -12))
	z := float32(math.Ldexp(1, -80))
	got, err := code.Call(execution.Words(uintptr(math.Float32bits(x)), uintptr(math.Float32bits(x)), uintptr(math.Float32bits(z)))...)
	want := uint64(math.Float32bits(float32(1 + math.Ldexp(1, -11) + math.Ldexp(1, -23))))
	doubleRounded := math.Float32bits(float32(math.FMA(float64(x), float64(x), float64(z))))
	if uint64(doubleRounded) == want {
		t.Fatal("test case does not distinguish double rounding")
	}
	if err != nil || uint64(got) != want {
		t.Fatalf("single FMA=%#x,%v; want %#x (double-rounded %#x)", got, err, want, doubleRounded)
	}
}

func TestPPC64SharedFloatingProductKeepsSeparateRounding(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		b := nyanjit.NewBuilder()
		x, y, z := b.ParamBits(width), b.ParamBits(width), b.ParamBits(width)
		product := x.FMul(y)
		b.Return(product.FAdd(z).FAdd(product.FAdd(z)))
		program := finishPPC(t, b)
		assembly, err := lowerListing(program, compile.TargetSpec{}, compile.Func{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(assembly, "fmadd") || strings.Count(assembly, "fmul") != 1 {
			t.Fatalf("shared multiply was contracted or duplicated:\n%s", assembly)
		}
		code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{})
		if err != nil {
			t.Fatal(err)
		}
		defer code.Close()
		xbits, ybits, zbits := math.Float64bits(1+math.Ldexp(1, -27)), math.Float64bits(1-math.Ldexp(1, -27)), math.Float64bits(-1)
		if width == 32 {
			xbits = uint64(math.Float32bits(1 + float32(math.Ldexp(1, -13))))
			ybits = uint64(math.Float32bits(1 - float32(math.Ldexp(1, -13))))
			zbits = uint64(math.Float32bits(-1))
		}
		got, err := code.Call(execution.Words(uintptr(xbits), uintptr(ybits), uintptr(zbits))...)
		if err != nil || got != 0 {
			t.Fatalf("shared product returned %#x,%v; want zero", got, err)
		}
	}
}

func TestPPC64FloatingLabelArgumentLoop(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		b := nyanjit.NewBuilder()
		count, step := b.ParamBits(32), b.ParamBits(width)
		loop, done := b.NewLabel(), b.NewLabel()
		left, acc := b.Var(count), b.Var(b.ConstBits(width, 0))
		b.Bind(loop)
		remaining, accumulator, increment := b.Get(left), b.Get(acc), step
		b.Branch(b.IsZero(remaining), done)
		b.Set(left, b.Sub(remaining, b.ConstBits(32, 1)))
		b.Set(acc, b.FAdd(accumulator, increment))
		b.Jump(loop)
		b.Bind(done)
		b.Return(accumulator)
		code, _ := compilePPC(t, b, compile.Func{})
		stepbits := math.Float64bits(0.5)
		if width == 32 {
			stepbits = uint64(math.Float32bits(0.5))
		}
		for _, n := range []uint64{0, 1, 20, 100} {
			want := math.Float64bits(float64(n) * 0.5)
			if width == 32 {
				want = uint64(math.Float32bits(float32(n) * 0.5))
			}
			got, err := code.Call(execution.Words(uintptr(n), uintptr(stepbits))...)
			if err != nil || uint64(got) != want {
				t.Fatalf("width %d sum %d = %#x,%v; want %#x", width, n, got, err, want)
			}
		}
	}
}

func TestPPC64FloatingRegisterPressure(t *testing.T) {
	for _, width := range []uint8{32, 64} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			b := nyanjit.NewBuilder()
			x := b.ParamBits(width)
			b.Enter()
			values := make([]nyanjit.Value, 32)
			for i := range values {
				constant := b.Float64(float64(i + 1))
				if width == 32 {
					constant = b.Float32(float32(i + 1))
				}
				values[i] = b.FMul(x, constant)
			}
			b.Leave(values...)
			result := values[0]
			for _, value := range values[1:] {
				result = b.FAdd(result, value)
			}
			b.Return(result)
			options := compile.Func{Stack: compile.StackPolicy{Practice: stack}}
			if stack == abi.StackPlatform {
				options.Stack.BytesAvailable = 4096
			}
			code, report := compilePPC(t, b, options)
			if report.SpillStorage == abi.SpillNone {
				t.Fatal("pressure test did not exercise spills")
			}
			input, want := math.Float64bits(0.5), math.Float64bits(264)
			if width == 32 {
				input = uint64(math.Float32bits(0.5))
				want = uint64(math.Float32bits(264))
			}
			got, err := code.Call(execution.Words(uintptr(input))...)
			if err != nil || uint64(got) != want {
				t.Fatalf("width %d stack %d pressure = %#x,%v; want %#x", width, stack, got, err, want)
			}
		}
	}
}
