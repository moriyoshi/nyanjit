//go:build linux && loong64

package compile_test

import (
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

func loongFloatProgram(t *testing.T, width uint8, operation string, expression bool) (*nyanjit.Builder, func(uint64, uint64, uint64) uint64) {
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

func loongFloatEqual(width uint8, got, want uint64) bool {
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

func TestLoong64FloatingArithmeticAndFMA(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		values := []uint64{0, 0x8000000000000000, math.Float64bits(1), math.Float64bits(-3.5), 1, 0x8000000000000001, 0x7fefffffffffffff, 0x0010000000000000, 0x000fffffffffffff, 0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000001, 0x7ff0000000000001}
		if w == 32 {
			values = []uint64{0, 0x80000000, uint64(math.Float32bits(1)), uint64(math.Float32bits(-3.5)), 1, 0x80000001, 0x7f7fffff, 0x00800000, 0x007fffff, 0x7f800000, 0xff800000, 0x7fc00001, 0x7f800001}
		}
		for _, op := range []string{"add", "sub", "mul", "div", "fma"} {
			t.Run(fmt.Sprintf("%d/%s", w, op), func(t *testing.T) {
				b, ref := loongFloatProgram(t, w, op, false)
				p := finishCrossAssembly(t, b)
				report, err := lowerReport(p, compile.TargetSpec{}, compile.Func{})
				if err != nil {
					t.Fatal(err)
				}
				code, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{})
				if err != nil {
					t.Fatal(err)
				}
				defer code.Close()
				if !report.FPStatusModified() || report.FlagsClobbered() || !slices.Contains(report.ClobberedSpecialRegisters(), "FCSR0") {
					t.Fatalf("wrong status report %+v", report.Snapshot())
				}
				for _, r := range []string{"R12", "R19", "R20"} {
					if slices.Contains(report.ClobberedRegisters(), r) {
						t.Fatalf("FP leaf falsely clobbers %s", r)
					}
				}
				check := func(x, y, z uint64) {
					args := []uintptr{uintptr(x), uintptr(y)}
					if op == "fma" {
						args = append(args, uintptr(z))
					}
					got, err := code.Call(args...)
					want := ref(x, y, z)
					if err != nil || !loongFloatEqual(w, uint64(got), want) {
						t.Fatalf("%s%d(%x,%x,%x)=%x,%v want%x", op, w, x, y, z, got, err, want)
					}
				}
				for a, x := range values {
					for c, y := range values {
						check(x, y, values[(a+c)%len(values)])
					}
				}
				rng := rand.New(rand.NewPCG(27, 64))
				for n := 0; n < 300; n++ {
					x, y, z := rng.Uint64(), rng.Uint64(), rng.Uint64()
					if w == 32 {
						x, y, z = uint64(uint32(x)), uint64(uint32(y)), uint64(uint32(z))
					}
					check(x, y, z)
				}
			})
		}
	}
}
func TestLoong64FMAContraction(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, staged := range []bool{false, true} {
			b := nyanjit.NewBuilder()
			x, y, z := b.ParamBits(w), b.ParamBits(w), b.ParamBits(w)
			v := x.FMul(y).FAdd(z)
			if staged {
				v = b.FAdd(b.FMul(x, y), z)
			}
			b.Return(v)
			p := finishCrossAssembly(t, b)
			text, err := lowerListing(p, compile.TargetSpec{}, compile.Func{})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(text, "fmadd.") == staged {
				t.Fatalf("contraction staged%v: %s", staged, text)
			}
			c, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{})
			if err != nil {
				t.Fatal(err)
			}
			xbits, ybits, zbits, want := fpBits(w, 1+math.Ldexp(1, -27)), fpBits(w, 1-math.Ldexp(1, -27)), fpBits(w, -1), fpBits(w, -math.Ldexp(1, -54))
			if w == 32 {
				xbits, ybits, want = fpBits(w, 1+math.Ldexp(1, -13)), fpBits(w, 1-math.Ldexp(1, -13)), fpBits(w, -math.Ldexp(1, -26))
			}
			if staged {
				want = 0
			}
			got, err := c.Call(uintptr(xbits), uintptr(ybits), uintptr(zbits))
			c.Close()
			if err != nil || uint64(got) != want {
				t.Fatalf("width%d staged%v got%x,%v want%x", w, staged, got, err, want)
			}
		}
	}
	b, _ := loongFloatProgram(t, 32, "fma", false)
	p := finishCrossAssembly(t, b)
	c, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	x, z := float32(1+math.Ldexp(1, -12)), float32(math.Ldexp(1, -80))
	got, err := c.Call(uintptr(math.Float32bits(x)), uintptr(math.Float32bits(x)), uintptr(math.Float32bits(z)))
	want := math.Float32bits(float32(1 + math.Ldexp(1, -11) + math.Ldexp(1, -23)))
	if err != nil || uint64(got) != uint64(want) {
		t.Fatalf("FP32 single-round FMA %x,%v want%x", got, err, want)
	}
}

func TestLoong64FloatUnaryAndConversion(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, convert := range []bool{false, true} {
			b := nyanjit.NewBuilder()
			x := b.ParamBits(w)
			out := b.FSqrt(x)
			outWidth := w
			if convert {
				outWidth = 96 - w
				out = b.FConvert(x, outWidth)
			}
			b.Return(out)
			p := finishCrossAssembly(t, b)
			c, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{})
			if err != nil {
				t.Fatal(err)
			}
			values := []uint64{0, fpBits(w, math.Copysign(0, -1)), fpBits(w, 1), fpBits(w, -3.5), fpBits(w, math.Inf(1)), fpBits(w, math.Inf(-1)), fpBits(w, math.NaN()), 1}
			rng := rand.New(rand.NewPCG(128, 64))
			for n := 0; n < 100; n++ {
				v := rng.Uint64()
				if w == 32 {
					v = uint64(uint32(v))
				}
				values = append(values, v)
			}
			for _, v := range values {
				x := math.Float64frombits(v)
				if w == 32 {
					x = float64(math.Float32frombits(uint32(v)))
				}
				want := fpBits(outWidth, math.Sqrt(x))
				if convert {
					want = fpBits(outWidth, x)
				}
				got, err := c.Call(uintptr(v))
				if err != nil || !loongFloatEqual(outWidth, uint64(got), want) {
					t.Fatalf("width%d convert%v input%x got%x,%v want%x", w, convert, v, got, err, want)
				}
			}
			c.Close()
		}
	}
}
