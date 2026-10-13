//go:build linux && (amd64 || arm64 || riscv64 || ppc64le)

package goregister_test

import (
	"errors"
	"reflect"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/goregister"
)

const managedTestStackBytes = 640

func compileMixedCall(t *testing.T, entry nyanjit.ABIFlavor, callGo bool, address uintptr) *compile.Code {
	t.Helper()
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := builder.NewBuilder()
	arg, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	target, err := b.Const(uint64(address))
	if err != nil {
		t.Fatal(err)
	}
	var result builder.Value
	if callGo {
		result, err = b.CallGo(target, arg)
	} else {
		result, err = b.CallC(target, arg)
	}
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
	code, err := compile.CompileGo(program, nyanjit.CompileOptions{
		ABI: entry, Stack: nyanjit.StackPlatform, StackBytesAvailable: managedTestStackBytes, AssumePointerFree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestManagedCallUsesFullCArity(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	count := abi.MaxArguments()
	calleeBuilder := builder.NewBuilder()
	calleeArgs := make([]builder.Value, count)
	for i := range calleeArgs {
		var err error
		calleeArgs[i], err = calleeBuilder.Param()
		if err != nil {
			t.Fatal(err)
		}
	}
	sum := calleeArgs[0]
	for _, arg := range calleeArgs[1:] {
		var err error
		sum, err = calleeBuilder.Add(sum, arg)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := calleeBuilder.Return(sum); err != nil {
		t.Fatal(err)
	}
	calleeProgram, err := calleeBuilder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	callee, err := compile.Compile(calleeProgram, nyanjit.CompileOptions{ABI: nyanjit.ABIC})
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	address, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	callerBuilder := builder.NewBuilder()
	callerArgs := make([]builder.Value, count)
	for i := range callerArgs {
		var err error
		callerArgs[i], err = callerBuilder.Param()
		if err != nil {
			t.Fatal(err)
		}
	}
	target, err := callerBuilder.Const(uint64(address))
	if err != nil {
		t.Fatal(err)
	}
	result, err := callerBuilder.CallC(target, callerArgs...)
	if err != nil {
		t.Fatal(err)
	}
	if err := callerBuilder.Return(result); err != nil {
		t.Fatal(err)
	}
	callerProgram, err := callerBuilder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	args := make([]uintptr, count)
	for i := range args {
		args[i] = uintptr(i + 1)
	}
	want := uint64(count * (count + 1) / 2)
	for _, entry := range []nyanjit.ABIFlavor{nyanjit.ABIGo, nyanjit.ABIC} {
		caller, err := compile.CompileGo(callerProgram, nyanjit.CompileOptions{
			ABI: entry, Stack: nyanjit.StackPlatform, StackBytesAvailable: managedTestStackBytes, AssumePointerFree: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		got, callErr := caller.Call(args...)
		_ = caller.Close()
		if callErr != nil || uint64(got) != want {
			t.Fatalf("entry %d full arity C call = %d, %v; want %d", entry, got, callErr, want)
		}
	}
}

func testManagedGoTarget(value uint64) uint64 {
	runtime.GC()
	return value + 7
}

func TestCEntryCallsGoInManagedContext(t *testing.T) {
	code := compileMixedCall(t, nyanjit.ABIC, true, reflect.ValueOf(testManagedGoTarget).Pointer())
	defer code.Close()
	got, err := code.Call(35)
	if err != nil || got != 42 {
		t.Fatalf("C entry to Go = %d, %v; want 42", got, err)
	}
}

func TestManagedMixedCallChainPreservesLiveValue(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	callee, err := testNewAdd()
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	address, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	b := builder.NewBuilder()
	input, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	cTarget, err := b.Const(uint64(address))
	if err != nil {
		t.Fatal(err)
	}
	goTarget, err := b.Const(uint64(reflect.ValueOf(testManagedGoTarget).Pointer()))
	if err != nil {
		t.Fatal(err)
	}
	one, err := b.Const(1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := b.CallC(cTarget, input, one)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.CallGo(goTarget, first)
	if err != nil {
		t.Fatal(err)
	}
	last, err := b.CallC(cTarget, second, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(last); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []nyanjit.ABIFlavor{nyanjit.ABIGo, nyanjit.ABIC} {
		code, err := compile.CompileGo(program, nyanjit.CompileOptions{
			ABI: entry, Stack: nyanjit.StackPlatform, StackBytesAvailable: managedTestStackBytes, AssumePointerFree: true,
		})
		if err != nil {
			t.Fatalf("entry %d: %v", entry, err)
		}
		got, callErr := code.Call(33)
		_ = code.Close()
		if callErr != nil || got != 74 {
			t.Fatalf("entry %d, C to Go to C = %d, %v; want 74", entry, got, callErr)
		}
	}
}

func TestCEntryCalledFromRuntimeCallbackTrampoline(t *testing.T) {
	code := compileMixedCall(t, nyanjit.ABIC, true, reflect.ValueOf(testManagedGoTarget).Pointer())
	defer code.Close()
	callback, err := execution.RegisterForeignCallback(func(value uintptr) uintptr {
		result, callErr := code.Call(value)
		if callErr != nil {
			panic(callErr)
		}
		return result
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := execution.CallForeign(callback, []uintptr{35}); got != 42 {
		t.Fatalf("C callback to managed JIT to Go = %d, want 42", got)
	}
}

func TestForeignContextRejectsDirectGoCall(t *testing.T) {
	b := builder.NewBuilder()
	target, err := b.Const(uint64(reflect.ValueOf(testManagedGoTarget).Pointer()))
	if err != nil {
		t.Fatal(err)
	}
	arg, err := b.Const(35)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallGo(target, arg)
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
	_, err = compile.PrepareForeign(program, nyanjit.CompileOptions{ABI: nyanjit.ABIC, Stack: nyanjit.StackPlatform, StackBytesAvailable: managedTestStackBytes})
	if !errors.Is(err, compile.ErrExternalCallOnly) {
		t.Fatalf("unmanaged foreign to Go call = %v, want ErrExternalCallOnly", err)
	}
}

func TestGoEntryCallsCThroughRuntimeGateway(t *testing.T) {
	callee, err := testNewAdd()
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	address, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	b := builder.NewBuilder()
	arg, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	seven, err := b.Const(7)
	if err != nil {
		t.Fatal(err)
	}
	target, err := b.Const(uint64(address))
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallC(target, arg, seven)
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
	caller, err := compile.CompileGo(program, nyanjit.CompileOptions{
		ABI: nyanjit.ABIGo, Stack: nyanjit.StackPlatform, StackBytesAvailable: managedTestStackBytes, AssumePointerFree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	got, err := caller.Call(35)
	if err != nil || got != 42 {
		t.Fatalf("Go entry to C = %d, %v; want 42", got, err)
	}
}

func TestManagedZeroArgumentCCall(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	callee, err := testNewReturn(42)
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	address, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	b := builder.NewBuilder()
	target, err := b.Const(uint64(address))
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallC(target)
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
	for _, entry := range []nyanjit.ABIFlavor{nyanjit.ABIGo, nyanjit.ABIC} {
		code, err := compile.CompileGo(program, nyanjit.CompileOptions{
			ABI: entry, Stack: nyanjit.StackPlatform, StackBytesAvailable: managedTestStackBytes, AssumePointerFree: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		got, callErr := code.Call()
		_ = code.Close()
		if callErr != nil || got != 42 {
			t.Fatalf("entry %d zero-arg C call = %d, %v", entry, got, callErr)
		}
	}
}

func TestCEntryBranchesToGoAndCCalls(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	callee, err := testNewAdd()
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	cAddress, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	b := builder.NewBuilder()
	entry, goPath, cPath := b.NewLabel(), b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	arg, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := b.IsZero(arg)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BranchArgs(predicate, goPath, []builder.Value{arg}, cPath, []builder.Value{arg}); err != nil {
		t.Fatal(err)
	}
	goArgs, err := b.BindParams(goPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	goTarget, err := b.Const(uint64(reflect.ValueOf(testManagedGoTarget).Pointer()))
	if err != nil {
		t.Fatal(err)
	}
	goResult, err := b.CallGo(goTarget, goArgs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(goResult); err != nil {
		t.Fatal(err)
	}
	cArgs, err := b.BindParams(cPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	cTarget, err := b.Const(uint64(cAddress))
	if err != nil {
		t.Fatal(err)
	}
	seven, err := b.Const(7)
	if err != nil {
		t.Fatal(err)
	}
	cResult, err := b.CallC(cTarget, cArgs[0], seven)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(cResult); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.CompileGo(program, nyanjit.CompileOptions{
		ABI: nyanjit.ABIC, Stack: nyanjit.StackPlatform, StackBytesAvailable: managedTestStackBytes, AssumePointerFree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, tc := range []struct{ input, want uint64 }{{0, 7}, {35, 42}} {
		got, err := code.Call(uintptr(tc.input))
		if err != nil || uint64(got) != tc.want {
			t.Fatalf("Call(%d) = %d, %v; want %d", tc.input, got, err, tc.want)
		}
	}
}
