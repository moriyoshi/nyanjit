//go:build linux && (amd64 || 386 || arm64 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"fmt"
	"math"
	"reflect"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func fpBits(w uint8, x float64) uint64 {
	if w == 32 {
		return uint64(math.Float32bits(float32(x)))
	}
	return math.Float64bits(x)
}
func fpConstant(b *nyanjit.Builder, w uint8, x float64) nyanjit.Value {
	if w == 32 {
		return b.Float32(float32(x))
	}
	return b.Float64(x)
}
func TestFPResidencyPressureAndIntegerBoundaries(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			t.Run(fmt.Sprintf("%d/%d", w, stack), func(t *testing.T) {
				b := nyanjit.NewBuilder()
				x := b.ParamBits(w)
				vs := make([]nyanjit.Value, 40)
				for i := range vs {
					vs[i] = b.FAdd(x, fpConstant(b, w, float64(i+1)))
				}
				sum := vs[0]
				for _, v := range vs[1:] {
					sum = b.FSub(v, sum)
				}
				// Integer bit manipulation and Select must observe the current FP bits.
				bits := b.Xor(sum, b.ConstBits(w, 1))
				result := b.Select(b.Literal(1), bits, b.ConstBits(w, 0))
				b.Return(b.CastUnsigned(result, 64))
				p, err := testFinishRaw(b)
				if err != nil {
					t.Fatal(err)
				}
				budget := 0
				if stack == abi.StackPlatform {
					budget = 4096
				}
				fn := compile.Func{Stack: compile.StackPolicy{Practice: stack, BytesAvailable: budget}}
				if stack == abi.StackPlatform {
					fn.Context = compile.ContextForeign
				}
				template, err := lowerTemplate(p, compile.TargetSpec{}, fn)
				if err != nil {
					t.Fatal(err)
				}
				code, err := template.Bind(nil)
				if err != nil {
					t.Fatal(err)
				}
				defer code.Close()
				want := 4.0
				for i := 1; i < 40; i++ {
					want = 3 + float64(i+1) - want
				}
				got, err := callMathBits(code, w, fpBits(w, 3))
				if err != nil || uint64(got) != fpBits(w, want)^1 {
					t.Fatalf("got%#x,%v want%#x", got, err, fpBits(w, want)^1)
				}
				if template.Report().SpillStorage() == abi.SpillNone {
					t.Fatal("pressure test did not spill")
				}
			})
		}
	}
}

//go:noinline
func fpGrowAndGC(value uint64) uint64 {
	var frame [2048]uint64
	for i := range frame {
		frame[i] = value + uint64(i)
	}
	runtime.GC()
	runtime.KeepAlive(frame)
	return value
}
func TestFPResidencyAcrossManagedCalls(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, entry := range []abi.Flavor{abi.C, abi.Go} {
			b := nyanjit.NewBuilder()
			x := b.ParamBits(w)
			vs := make([]nyanjit.Value, 8)
			for i := range vs {
				vs[i] = b.FAdd(x, fpConstant(b, w, float64(i+1)))
			}
			called := b.CallGo(b.ConstNative(reflect.ValueOf(fpGrowAndGC).Pointer()), b.Const(42))
			sum := vs[0]
			for _, v := range vs[1:] {
				sum = b.FAdd(sum, v)
			}
			b.Return(b.Xor(b.CastUnsigned(sum, 64), called))
			p, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			code, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{ABI: entry, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}, AssumePointerFree: true})
			if managedUnsupported(err) {
				t.Skip("Go-managed code needs goregister:", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := callMathBits(code, w, fpBits(w, 3))
			code.Close()
			if err != nil || uint64(got) != fpBits(w, 60)^42 {
				t.Fatalf("width%d entry%d got%#x,%v want%#x", w, entry, got, err, fpBits(w, 60)^42)
			}
		}
	}
}
func TestFPResidencyLabelsAndScopes(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		b := nyanjit.NewBuilder()
		x := b.ParamBits(w)
		loop, done := b.NewLabel(), b.NewLabel()
		count, value := b.Var(b.ConstNative(3)), b.Var(x)
		b.Bind(loop)
		b.Enter()
		y := b.FAdd(b.Get(value), fpConstant(b, w, .5))
		b.Leave(y)
		n := b.Sub(b.Get(count), b.ConstNative(1))
		b.Branch(b.IsZero(n), done)
		b.Set(count, n)
		b.Set(value, y)
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.CastUnsigned(y, 64))
		p, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		code, err := nyanjit.Compile(p, compile.TargetSpec{}, compile.Func{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := callMathBits(code, w, fpBits(w, 2))
		code.Close()
		if err != nil || uint64(got) != fpBits(w, 3.5) {
			t.Fatalf("width%d got%#x,%v", w, got, err)
		}
	}
}

func TestFPAndSIMDShareRegisterOwnership(t *testing.T) {
	if runtime.GOARCH == "386" {
		t.Skip("386 SIMD is scalarized; it has no native SIMD register homes")
	}
	m, address := simdMemory(t)
	for i := 0; i < 256; i++ {
		m[1+i] = byte(i*37 + 9)
	}
	b := nyanjit.NewBuilder()
	p, out, x := b.ParamNative(), b.ParamNative(), b.Param()
	vectors := make([]nyanjit.Value, 16)
	for i := range vectors {
		vectors[i] = b.VLoad(ir.Vec(2, 64), b.Add(p, b.ConstNative(uintptr(i*16))))
	}
	floats := make([]nyanjit.Value, 20)
	for i := range floats {
		floats[i] = b.FAdd(x, b.Float64(float64(i+1)))
	}
	for i, v := range vectors {
		b.VStore(b.Add(out, b.ConstNative(uintptr(i*16))), v)
	}
	sum := floats[0]
	for _, v := range floats[1:] {
		sum = b.FAdd(sum, v)
	}
	b.Return(sum)
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call(uintptr(address+1), uintptr(address+1025), uintptr(math.Float64bits(2)))
	if err != nil || uint64(got) != math.Float64bits(250) {
		t.Fatalf("FP bank overlap got%#x,%v", got, err)
	}
	for i := 0; i < 256; i++ {
		if m[1025+i] != m[1+i] {
			t.Fatalf("FP value overwrote SIMD byte%d", i)
		}
	}
}
