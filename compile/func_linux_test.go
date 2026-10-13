//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"errors"
	"math"
	"reflect"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/goregister"
)

func funcTestOptions() nyanjit.CompileOptions {
	return nyanjit.CompileOptions{ABI: nyanjit.ABIGo, Stack: nyanjit.StackPlatform, StackBytesAvailable: 240}
}

// mixedWidthProgram returns uint64(a) + 2*b + 3*c for a 32-bit a, a 64-bit b
// and a native c. On 386 the parameters occupy one, two and one stack words.
func mixedWidthProgram(t *testing.T) *nyanjit.Builder {
	t.Helper()
	b := nyanjit.NewBuilder()
	a := b.CastUnsigned(b.ParamBits(32), 64)
	x := b.Param()
	c := b.CastUnsigned(b.ParamNative(), 64)
	b.Return(b.Add(b.Add(a, b.Mul(x, b.Const(2))), b.Mul(c, b.Const(3))))
	return b
}

func TestFuncCallsGoEntryDirectly(t *testing.T) {
	b := mixedWidthProgram(t)
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.Compile(program, funcTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	f, err := execution.Func[func(uint32, uint64, uintptr) uint64](code)
	if err != nil {
		t.Fatal(err)
	}
	if got := f(0xffffffff, 1<<40, 7); got != 0xffffffff+2<<40+21 {
		t.Fatalf("direct call = %#x", got)
	}
	// The trampoline and the func value must agree.
	lo, hi, err := code.CallPair(execution.Words("lLl", 0xffffffff, 1<<40, 7)...)
	if err != nil || execution.Wide(lo, hi) != 0xffffffff+2<<40+21 {
		t.Fatalf("CallPair = %#x, %v", execution.Wide(lo, hi), err)
	}
}

//go:noinline
func zeroedAfter(f func(uint64, uint64) uint64, x, y uint64, buf *[96]byte, word *uint64) uint64 {
	result := f(x, y)
	// Go clears memory through its fixed zero registers (X15 on amd64, R0 on
	// PPC64), so these stores after the call fail if the callee left them dirty.
	*buf = [96]byte{}
	*word = 0
	return result
}

func TestFuncPreservesGoFixedRegisters(t *testing.T) {
	if runtime.GOARCH == "386" {
		t.Skip("binary64 FP chain needs a 64-bit host")
	}
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	// A long FP chain occupies the FP/vector file.
	v := x
	for i := 0; i < 24; i++ {
		v = b.FAdd(b.FMul(v, y), b.Float64(float64(i)))
	}
	b.Return(v)
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.Compile(program, funcTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	f, err := execution.Func[func(uint64, uint64) uint64](code)
	if err != nil {
		t.Fatal(err)
	}
	want := 1.5
	for i := 0; i < 24; i++ {
		want = want*0.5 + float64(i)
	}
	for range 100 {
		var buf [96]byte
		for i := range buf {
			buf[i] = 0xa5
		}
		word := ^uint64(0)
		got := zeroedAfter(f, math.Float64bits(1.5), math.Float64bits(0.5), &buf, &word)
		if math.Float64frombits(got) != want || buf != [96]byte{} || word != 0 {
			t.Fatalf("result %v (want %v), cleared buffer %v, cleared word %#x", math.Float64frombits(got), want, buf == [96]byte{}, word)
		}
	}
}

//go:noinline
func funcCallbackGrows(depth, value uint64) uint64 {
	var frame [512]byte
	frame[depth%512] = byte(value)
	if depth > 0 {
		return funcCallbackGrows(depth-1, value) + uint64(frame[depth%512]-byte(value))
	}
	runtime.GC()
	return value + 1
}

func funcCallbackTarget(value uint64) uint64 { return funcCallbackGrows(64, value) }

func TestFuncManagedEntryGrowsStackAndRunsGC(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := nyanjit.NewBuilder()
	x := b.Param()
	target := b.ConstNative(reflect.ValueOf(funcCallbackTarget).Pointer())
	b.Return(b.Add(b.CallGo(target, x), b.Const(10)))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	options := funcTestOptions()
	options.StackBytesAvailable = 512
	options.AssumePointerFree = true
	code, err := compile.CompileGo(program, options)
	if err != nil {
		t.Fatal(err)
	}
	f, err := execution.Func[func(uint64) uint64](code)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan uint64)
	// A fresh goroutine starts with a small stack, forcing growth and
	// copying while the generated frame is on it.
	go func() { done <- f(31) }()
	if got := <-done; got != 42 {
		t.Fatalf("managed direct call = %d", got)
	}
}

func TestFuncRejectsMismatchedSignatures(t *testing.T) {
	b := mixedWidthProgram(t)
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.Compile(program, funcTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, execution.ErrSignature) {
			t.Fatalf("%s: %v; want ErrSignature", name, err)
		}
	}
	_, err = execution.Func[func(uint64, uint64, uintptr) uint64](code)
	check("wide first parameter", err)
	_, err = execution.Func[func(uint32, float64, uintptr) uint64](code)
	check("float parameter", err)
	_, err = execution.Func[func(uint32, uint64) uint64](code)
	check("missing parameter", err)
	_, err = execution.Func[func(uint32, uint64, uintptr) (uint64, uint64)](code)
	check("two results", err)
	_, err = execution.Func[func(uint32, uint64, uintptr) float64](code)
	check("float result", err)
	_, err = execution.Func[func(uint32, uint64, ...uintptr) uint64](code)
	check("variadic", err)
	_, err = execution.Func[uint64](code)
	check("not a function", err)

	cOptions := funcTestOptions()
	cOptions.ABI = nyanjit.ABIC
	cCode, err := compile.Compile(program, cOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer cCode.Close()
	if _, err := execution.Func[func(uint32, uint64, uintptr) uint64](cCode); !errors.Is(err, execution.ErrInvalidCodeOptions) {
		t.Fatalf("C ABI code: %v; want ErrInvalidCodeOptions", err)
	}
	_ = code.Close()
	if _, err := execution.Func[func(uint32, uint64, uintptr) uint64](code); !errors.Is(err, execution.ErrClosed) {
		t.Fatalf("closed code: %v; want ErrClosed", err)
	}
}
