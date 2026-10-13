//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"fmt"
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"math"
	"reflect"
	"runtime"
	"testing"
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
				p, err := b.Finish()
				if err != nil {
					t.Fatal(err)
				}
				budget := 0
				if stack == abi.StackPlatform {
					budget = 4096
				}
				template, err := compile.Prepare(p, abi.CompileOptions{Stack: stack, StackBytesAvailable: budget})
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
				got, err := code.Call(uintptr(fpBits(w, 3)))
				if err != nil || uint64(got) != fpBits(w, want)^1 {
					t.Fatalf("got%#x,%v want%#x", got, err, fpBits(w, want)^1)
				}
				if template.Report().SpillStorage == abi.SpillNone {
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
			called := b.CallGo(b.Const(uint64(reflect.ValueOf(fpGrowAndGC).Pointer())), b.Const(42))
			sum := vs[0]
			for _, v := range vs[1:] {
				sum = b.FAdd(sum, v)
			}
			b.Return(b.Xor(b.CastUnsigned(sum, 64), called))
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			code, err := compile.CompileGo(p, abi.CompileOptions{ABI: entry, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true})
			if err != nil {
				t.Fatal(err)
			}
			got, err := code.Call(uintptr(fpBits(w, 3)))
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
		b.JumpArgs(loop, b.ConstNative(3), x)
		ps := b.BindParamsTypes(loop, nyanjit.Scalar(nyanjit.NativeBits), nyanjit.Scalar(w))
		b.Enter()
		y := b.FAdd(ps[1], fpConstant(b, w, .5))
		b.Leave(y)
		n := b.Sub(ps[0], b.ConstNative(1))
		b.BranchArgs(b.IsZero(n), done, []nyanjit.Value{y}, loop, []nyanjit.Value{n, y})
		last := b.BindParamsTypes(done, nyanjit.Scalar(w))
		b.Return(b.CastUnsigned(last[0], 64))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		code, err := compile.Compile(p, abi.CompileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := code.Call(uintptr(fpBits(w, 2)))
		code.Close()
		if err != nil || uint64(got) != fpBits(w, 3.5) {
			t.Fatalf("width%d got%#x,%v", w, got, err)
		}
	}
}

func TestFPAndSIMDShareRegisterOwnership(t *testing.T) {
	m, address := simdMemory(t)
	for i := 0; i < 256; i++ {
		m[1+i] = byte(i*37 + 9)
	}
	b := nyanjit.NewBuilder()
	p, out, x := b.ParamNative(), b.ParamNative(), b.Param()
	vectors := make([]nyanjit.Value, 16)
	for i := range vectors {
		vectors[i] = b.VLoad(nyanjit.Vec(2, 64), b.Add(p, b.ConstNative(uintptr(i*16))))
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
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.Compile(program, abi.CompileOptions{})
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
