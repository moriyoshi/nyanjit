//go:build linux && amd64

package compile_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/foreign"
)

func TestSIMDGoZeroRegister(t *testing.T) {
	m, address := simdMemory(t)
	for i := 0; i < 300; i++ {
		m[1+i] = byte(0xA5 + i)
	}
	callback, err := foreign.RegisterCallback(func() uintptr { return uintptr(simdZeroSensitive(3)) })
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"go", "managed-c"} {
		b := nyanjit.NewBuilder()
		p, out := b.ParamNative(), b.ParamNative()
		shape := nyanjit.Vec(16, 8)
		values := make([]nyanjit.Value, 13)
		for i := range values {
			values[i] = b.VLoad(shape, b.Add(p, b.ConstNative(uintptr(i*16))))
		}
		r := values[0]
		for _, v := range values[1:] {
			r = b.Xor(r, v)
		}
		b.VStore(out, r)
		var called nyanjit.Value
		if kind == "go" {
			called = b.CallGo(b.Const(uint64(reflect.ValueOf(simdZeroSensitive).Pointer())), b.Const(3))
		} else {
			called, err = foreign.Call(b, callback)
			if err != nil {
				t.Fatal(err)
			}
		}
		b.Return(called)
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range []abi.CompileOptions{
			{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 240, AssumePointerFree: true},
			{ABI: abi.Go, Stack: abi.StackPlatform, StackBytesAvailable: 240, AssumePointerFree: true},
		} {
			lo := o
			lo.ManagedCalls = true
			listing, err := compile.EmitAssembly(program, lo)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(listing, "xmm15") {
				t.Fatalf("Go zero register allocated:\n%s", listing)
			}
			template, err := compile.PrepareGo(program, o)
			if err != nil {
				t.Fatal(err)
			}
			code, err := template.Bind(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			got, err := code.Call(uintptr(address+1), uintptr(address+1025))
			t.Logf("abi=%v callee returned %d (want 4) err=%v", o.ABI, got, err)
			if err != nil || got != 4 {
				t.Errorf("Go callee saw nonzero X15: got %d", got)
			}
		}
	}
}

//go:noinline
func simdZeroSensitive(idx uint64) uint64 {
	var a [32]uint64
	a[idx%32] = 1
	s := uint64(0)
	for i := range a {
		s += a[i] * uint64(i+1)
	}
	return s
}
