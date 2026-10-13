//go:build !nyanjit_no_goregister && linux && (amd64 || arm64 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func jitGCWhileCalled(value uint64) uint64 {
	runtime.GC()
	return value + 7
}

func compileGoScalarCall(t *testing.T, fn func(uint64) uint64) *execution.Code {
	t.Helper()
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := builder.NewBuilder()
	value, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	target, err := b.Const(uint64(reflect.ValueOf(fn).Pointer()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallGo(target, value)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}, AssumePointerFree: true})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestGoModuleScansGeneratedCaller(t *testing.T) {
	code := compileGoScalarCall(t, jitGCWhileCalled)
	defer code.Close()
	got, err := code.Call(execution.Words(35)...)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Fatalf("generated Go call = %d, want 42", got)
	}
}

func TestGoModuleRegistersFramelessLeaf(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := builder.NewBuilder()
	value, err := b.Const(42)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(value); err != nil {
		t.Fatal(err)
	}
	f := compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}, AssumePointerFree: true}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(program, compile.TargetSpec{}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := template.Report().StackBytes(); got != 0 {
		t.Fatalf("leaf stack bytes = %d, want zero", got)
	}
	code, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	entry, err := code.EntryAddress()
	if err != nil || runtime.FuncForPC(entry) == nil {
		t.Fatalf("frameless leaf entry=%#x err=%v has no Go function metadata", entry, err)
	}
	got, err := code.Call(execution.Words()...)
	if err != nil || got != 42 {
		t.Fatalf("frameless leaf = %d, %v; want 42", got, err)
	}
}

var jitReentryCode *execution.Code

func jitReenterGeneratedCode(value uint64) uint64 {
	result, err := jitReentryCode.Call(execution.Words(uintptr(value + 1))...)
	if err != nil {
		panic(err)
	}
	return uint64(result) + 1
}

func TestGoModuleReentersGeneratedCode(t *testing.T) {
	inner := compileGoScalarCall(t, jitGCWhileCalled)
	defer inner.Close()
	jitReentryCode = inner
	defer func() { jitReentryCode = nil }()
	outer := compileGoScalarCall(t, jitReenterGeneratedCode)
	defer outer.Close()
	got, err := outer.Call(execution.Words(33)...)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Fatalf("reentered generated Go call = %d, want 42", got)
	}
}

//go:noinline
func jitGrowStack(depth uint64) uint64 {
	var frame [1024]byte
	frame[0] = byte(depth)
	if depth == 0 {
		runtime.GC()
		runtime.KeepAlive(frame)
		return 7
	}
	result := jitGrowStack(depth-1) + 1
	runtime.KeepAlive(frame)
	return result
}

func TestGoModuleSurvivesStackGrowth(t *testing.T) {
	code := compileGoScalarCall(t, jitGrowStack)
	defer code.Close()
	got, err := code.Call(execution.Words(64)...)
	if err != nil {
		t.Fatal(err)
	}
	if got != 71 {
		t.Fatalf("generated Go call after stack growth = %d, want 71", got)
	}
}

func jitPanicsThroughGeneratedCaller(uint64) uint64 { panic("jit panic") }

func TestGoModuleUnwindsPanic(t *testing.T) {
	code := compileGoScalarCall(t, jitPanicsThroughGeneratedCaller)
	defer code.Close()
	defer func() {
		if got := recover(); got != "jit panic" {
			t.Errorf("recovered panic = %v, want jit panic", got)
		}
	}()
	_, _ = code.Call(execution.Words(0)...)
	t.Fatal("Go call returned without panicking")
}

func TestGoModuleCloseRetainsRuntimeMetadata(t *testing.T) {
	code := compileGoScalarCall(t, jitGCWhileCalled)
	entry, err := code.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	if err := code.Close(); err != nil {
		t.Fatal(err)
	}
	if fn := runtime.FuncForPC(entry); fn == nil || fn.Name() != "nyanjit.generated" {
		if fn == nil {
			t.Fatal("registered function disappeared after Close")
		}
		t.Fatalf("registered function after Close = %q at %#x", fn.Name(), fn.Entry())
	}
	if _, err := code.Call(execution.Words(0)...); err != execution.ErrClosed {
		t.Fatalf("Call after Close error = %v, want execution.ErrClosed", err)
	}
}

func TestGoModuleCallAcrossBuilderLabels(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := builder.NewBuilder()
	entry, exit := b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	target, err := b.Const(uint64(reflect.ValueOf(jitGCWhileCalled).Pointer()))
	if err != nil {
		t.Fatal(err)
	}
	input, err := b.Const(35)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallGo(target, input)
	if err != nil {
		t.Fatal(err)
	}
	// result crosses the edge by dominance.
	if err := b.Jump(exit); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(exit); err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}, AssumePointerFree: true})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call(execution.Words()...)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Fatalf("Go call across labels = %d, want 42", got)
	}
}

//go:noinline
func jitSixArgGrowStack(a, b, c, d, e, f uint64) uint64 {
	var frame [8192]byte
	frame[0] = byte(a)
	runtime.GC()
	runtime.KeepAlive(frame)
	return a + b + c + d + e + f
}

//go:noinline
func jitFourArgGrowStack(a, b, c, d uint64) uint64 {
	var frame [8192]byte
	frame[0] = byte(a)
	runtime.GC()
	runtime.KeepAlive(frame)
	return a + b + c + d
}

func TestGoCallReservesRegisterArgumentHomes(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := builder.NewBuilder()
	live, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	function, argumentCount, minimumFrame, want := any(jitSixArgGrowStack), 6, 56, uint64(26)
	if runtime.GOARCH == "arm64" {
		function, argumentCount, minimumFrame, want = jitFourArgGrowStack, 4, 80, 15
	}
	target, err := b.Const(uint64(reflect.ValueOf(function).Pointer()))
	if err != nil {
		t.Fatal(err)
	}
	args := make([]nyanjit.Value, argumentCount)
	for i := range args {
		args[i], err = b.Const(uint64(i + 1))
		if err != nil {
			t.Fatal(err)
		}
	}
	called, err := b.CallGo(target, args...)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := b.Add(live, called)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(sum); err != nil {
		t.Fatal(err)
	}
	f := compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 240}, AssumePointerFree: true}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	template, err := lowerTemplate(program, compile.TargetSpec{}, f)
	if err != nil {
		t.Fatal(err)
	}
	if got := template.Report().StackBytes(); got < minimumFrame {
		t.Fatalf("Go call frame = %d bytes, want at least %d", got, minimumFrame)
	}
	code, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	result := make(chan uint64, 1)
	go func() {
		value, callErr := code.Call(execution.Words(5)...)
		if callErr != nil {
			result <- ^uint64(0)
			return
		}
		result <- uint64(value)
	}()
	if got := <-result; got != want {
		t.Fatalf("%d-argument Go call = %d, want %d", argumentCount, got, want)
	}
}
