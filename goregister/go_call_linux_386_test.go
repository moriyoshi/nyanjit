//go:build linux && 386

package goregister_test

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/goregister"
)

//go:noinline
func go386AddSeven(value uint64) uint64 {
	runtime.GC()
	return value + 7
}

func Test386ManagedEntryCallsGo(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := builder.NewBuilder()
	input, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	target, err := b.ConstNative(reflect.ValueOf(go386AddSeven).Pointer())
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallGo(target, input)
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
	for _, entry := range []abi.Flavor{abi.C, abi.Go} {
		code, err := compile.CompileGo(program, abi.CompileOptions{
			ABI: entry, Stack: abi.StackPlatform, StackBytesAvailable: 240, AssumePointerFree: true,
		})
		if err != nil {
			t.Fatalf("entry %d: %v", entry, err)
		}
		lo, hi, callErr := code.CallPair(execution.Words("L", 35)...)
		_ = code.Close()
		if got := execution.Wide(lo, hi); callErr != nil || got != 42 {
			t.Fatalf("386 entry %d to Go = %d, %v; want 42", entry, got, callErr)
		}
	}
}

func Test386ManagedEntriesCallC(t *testing.T) {
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
	target, err := b.ConstNative(address)
	if err != nil {
		t.Fatal(err)
	}
	seven, err := b.Const(7)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallC(target, input, seven)
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
	for _, entry := range []abi.Flavor{abi.C, abi.Go} {
		code, err := compile.CompileGo(program, abi.CompileOptions{
			ABI: entry, Stack: abi.StackPlatform, StackBytesAvailable: 240, AssumePointerFree: true,
		})
		if err != nil {
			t.Fatalf("entry %d: %v", entry, err)
		}
		lo, hi, callErr := code.CallPair(execution.Words("L", 35)...)
		_ = code.Close()
		if got := execution.Wide(lo, hi); callErr != nil || got != 42 {
			t.Fatalf("386 entry %d to C = %d, %v; want 42", entry, got, callErr)
		}
	}
}

//go:noinline
func go386NineArguments(a, b, c, d, e, f, g, h, i uint64) uint64 {
	runtime.GC()
	return a + b + c + d + e + f + g + h + i
}

func Test386MaximumGoCallArity(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := builder.NewBuilder()
	target, err := b.ConstNative(reflect.ValueOf(go386NineArguments).Pointer())
	if err != nil {
		t.Fatal(err)
	}
	args := make([]builder.Value, 9)
	for i := range args {
		args[i], err = b.Const(uint64(i + 1))
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := b.CallGo(target, args...)
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
	for _, entry := range []abi.Flavor{abi.C, abi.Go} {
		code, err := compile.CompileGo(program, abi.CompileOptions{
			ABI: entry, Stack: abi.StackPlatform, StackBytesAvailable: 240, AssumePointerFree: true,
		})
		if err != nil {
			t.Fatalf("entry %d: %v", entry, err)
		}
		lo, hi, callErr := code.CallPair()
		_ = code.Close()
		if got := execution.Wide(lo, hi); callErr != nil || got != 45 {
			t.Fatalf("entry %d: result = %d, %v; want 45", entry, got, callErr)
		}
	}
}

func Test386ManagedCToGoCallback(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	callback, err := execution.RegisterForeignCallback(func(value uint64) uint64 {
		runtime.GC()
		return value + 7
	})
	if err != nil {
		t.Fatal(err)
	}
	b := builder.NewBuilder()
	input, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	target, err := b.ConstNative(callback)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallC(target, input)
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
	for _, entry := range []abi.Flavor{abi.C, abi.Go} {
		code, err := compile.CompileGo(program, abi.CompileOptions{
			ABI: entry, Stack: abi.StackPlatform, StackBytesAvailable: 240, AssumePointerFree: true,
		})
		if err != nil {
			t.Fatalf("entry %d: %v", entry, err)
		}
		lo, hi, callErr := code.CallPair(execution.Words("L", 35)...)
		_ = code.Close()
		if got := execution.Wide(lo, hi); callErr != nil || got != 42 {
			t.Fatalf("entry %d: result = %d, %v; want 42", entry, got, callErr)
		}
	}
}

func Test386ManagedGoEntryBranchResult(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	b := builder.NewBuilder()
	entry, yes, no := b.NewLabel(), b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	zero, err := b.ConstNative(0)
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := b.IsZero(zero)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Branch(predicate, yes, no); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(yes); err != nil {
		t.Fatal(err)
	}
	value, err := b.Const(42)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(value); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(no); err != nil {
		t.Fatal(err)
	}
	value, err = b.Const(7)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(value); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.CompileGo(program, abi.CompileOptions{
		ABI: abi.Go, Stack: abi.StackPlatform, StackBytesAvailable: 240, AssumePointerFree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	lo, hi, err := code.CallPair()
	if got := execution.Wide(lo, hi); err != nil || got != 42 {
		t.Fatalf("Go branch result = %d, %v; want 42", got, err)
	}
}
