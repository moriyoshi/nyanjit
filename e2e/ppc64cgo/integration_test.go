//go:build linux && (ppc64 || ppc64le)

package ppc64cgo

import (
	"os"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
)

func TestSharedLibraryCall(t *testing.T) {
	path := os.Getenv("NYANJIT_PPC_C_FIXTURE")
	if path == "" {
		t.Fatal("NYANJIT_PPC_C_FIXTURE must name the cross-compiled C shared library")
	}
	address := fixtureAddress(path)
	if address == 0 {
		t.Fatalf("cannot load native_add from %s", path)
	}
	for _, managed := range []bool{false, true} {
		b := nyanjit.NewBuilder()
		x := b.Param()
		b.Return(b.CallC(b.Const(uint64(address)), x))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		options := abi.CompileOptions{ABI: abi.C, PPC64CABI: abi.PPC64ELFv2, Stack: abi.StackPlatform, StackBytesAvailable: 640}
		var code *compile.Code
		if managed {
			options.ABI = abi.Go
			options.PPC64CABI = abi.PPC64CABIUnspecified
			options.AssumePointerFree = true
			code, err = compile.CompileGo(p, options)
		} else {
			code, err = compile.CompileForeign(p, options)
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := code.Call(35)
		_ = code.Close()
		if err != nil || got != 42 {
			t.Fatalf("managed=%t: result=%d, %v; want 42", managed, got, err)
		}
	}
}

func TestRuntimeGatewayWithCgo(t *testing.T) {
	fn, err := execution.RegisterForeignCallback(func(x uint64) uint64 { runtime.GC(); return x + 7 })
	if err != nil {
		t.Fatal(err)
	}
	if got := execution.CallForeign(fn, []uintptr{35}); got != 42 {
		t.Fatalf("runtime gateway = %d; want 42", got)
	}
	if got := invokeCallback(fn, 35); got != 42 {
		t.Fatalf("cgo callback = %d; want 42", got)
	}
}

func TestForeignCreatedThreadCallback(t *testing.T) {
	fn, err := execution.RegisterForeignCallback(func(x uint64) uint64 { runtime.GC(); runtime.Gosched(); return x + 7 })
	if err != nil {
		t.Fatal(err)
	}
	if got := invokeThreadCallback(fn, 28); got != 42 {
		t.Fatalf("foreign thread callback = %d; want 42", got)
	}
}

func TestForeignThreadReentersRegisteredJIT(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	b.Return(b.CallGo(b.Const(uint64(reflect.ValueOf(growGoStack).Pointer())), x))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.CompileGo(program, abi.CompileOptions{
		ABI: abi.Go, Stack: abi.StackPlatform, StackBytesAvailable: 640, AssumePointerFree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	fn, err := execution.RegisterForeignCallback(func(x uint64) uint64 {
		// Recover within Go; a panic must not unwind through pthread_join.
		func() {
			defer func() {
				if recover() != "foreign callback" {
					panic("callback recovery failed")
				}
			}()
			panic("foreign callback")
		}()
		result, callErr := code.Call(uintptr(x))
		if callErr != nil {
			panic(callErr)
		}
		return uint64(result)
	})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan uint64, 8)
	for i := 0; i < cap(results); i++ {
		go func() { results <- invokeThreadCallback(fn, 28) }()
	}
	for i := 0; i < cap(results); i++ {
		select {
		case got := <-results:
			if got != 42 {
				t.Errorf("C thread -> callback -> JIT -> Go = %d; want 42", got)
			}
		case <-time.After(time.Minute):
			t.Fatal("foreign-thread reentry timed out")
		}
	}
}

//go:noinline
func growGoStack(x uint64) uint64 { return growGoStackDepth(x, 64) }

//go:noinline
func growGoStackDepth(x uint64, depth int) uint64 {
	var frame [1024]byte
	frame[0] = byte(x)
	if depth == 0 {
		runtime.GC()
		return x + 7
	}
	got := growGoStackDepth(x, depth-1)
	runtime.KeepAlive(frame)
	return got
}
