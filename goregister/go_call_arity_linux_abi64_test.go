//go:build linux && (amd64 || arm64 || riscv64)

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
	"github.com/moriyoshi/nyanjit/goregister"
)

//go:noinline
func sevenGoArguments(a, b, c, d, e, f, g uint64) uint64 {
	runtime.GC()
	return a + 10*b + 100*c + 1_000*d + 10_000*e + 100_000*f + 1_000_000*g
}

//go:noinline
func nineGoArguments(a, b, c, d, e, f, g, h, i uint64) uint64 {
	runtime.GC()
	return a + 10*b + 100*c + 1_000*d + 10_000*e + 100_000*f + 1_000_000*g + 10_000_000*h + 100_000_000*i
}

func decimalArguments(args []uint64) uint64 {
	var result, place uint64 = 0, 1
	for _, arg := range args {
		result += arg * place
		place *= 10
	}
	return result
}

func goCallTestStackBytes() int {
	if runtime.GOARCH == "riscv64" {
		return 512
	}
	return 240
}

func TestMaximumGoCallArity(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	count := abi.MaxGoCallArguments()
	target := reflect.ValueOf(sevenGoArguments).Pointer()
	if count == 9 {
		target = reflect.ValueOf(nineGoArguments).Pointer()
	}
	b := builder.NewBuilder()
	address, err := b.Const(uint64(target))
	if err != nil {
		t.Fatal(err)
	}
	args := make([]builder.Value, count)
	for i := range args {
		args[i], err = b.Const(uint64(i + 1))
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := b.CallGo(address, args...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.CallGo(address, append(args, args[0])...); !errors.Is(err, builder.ErrBackendLimit) {
		t.Fatalf("Go call with %d arguments: %v; want backend limit", count+1, err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []abi.Flavor{abi.Go, abi.C} {
		code, err := compile.CompileGo(program, nyanjit.CompileOptions{
			ABI: entry, Stack: nyanjit.StackPlatform, StackBytesAvailable: goCallTestStackBytes(), AssumePointerFree: true,
		})
		if err != nil {
			t.Fatalf("entry %d: %v", entry, err)
		}
		got, callErr := code.Call()
		_ = code.Close()
		values := make([]uint64, count)
		for i := range values {
			values[i] = uint64(i + 1)
		}
		want := decimalArguments(values)
		if callErr != nil || uint64(got) != want {
			t.Fatalf("entry %d with %d Go arguments: %d, %v; want %d", entry, count, got, callErr, want)
		}
	}
}

func TestMaximumGoCallArityWithRegisterMoves(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	count := abi.MaxGoCallArguments()
	target := reflect.ValueOf(sevenGoArguments).Pointer()
	if count == 9 {
		target = reflect.ValueOf(nineGoArguments).Pointer()
	}
	b := builder.NewBuilder()
	first, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	address, err := b.Const(uint64(target))
	if err != nil {
		t.Fatal(err)
	}
	args := []builder.Value{second, first}
	values := []uint64{9, 8}
	for i := 2; i < count; i++ {
		value := uint64(i + 1)
		constant, constErr := b.Const(value)
		if constErr != nil {
			t.Fatal(constErr)
		}
		args = append(args, constant)
		values = append(values, value)
	}
	result, err := b.CallGo(address, args...)
	if err != nil {
		t.Fatal(err)
	}
	result, err = b.Add(result, first)
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
	for _, entry := range []abi.Flavor{abi.Go, abi.C} {
		code, err := compile.CompileGo(program, nyanjit.CompileOptions{
			ABI: entry, Stack: nyanjit.StackPlatform, StackBytesAvailable: goCallTestStackBytes(), AssumePointerFree: true,
		})
		if err != nil {
			t.Fatalf("entry %d: %v", entry, err)
		}
		got, callErr := code.Call(8, 9)
		_ = code.Close()
		want := decimalArguments(values) + 8
		if callErr != nil || uint64(got) != want {
			t.Fatalf("entry %d with register moves: %d, %v; want %d", entry, got, callErr, want)
		}
	}
}
