//go:build linux && (amd64 || arm64)

package compile_test

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/foreign"
	"github.com/moriyoshi/nyanjit/ir"
)

//go:noinline
func simdGoCallback(value uint64) uint64 {
	var frame [2048]uint64
	for i := range frame {
		frame[i] = value + uint64(i)
	}
	runtime.GC()
	runtime.KeepAlive(frame)
	return value + 1
}
func TestSIMDLiveAcrossCAndGoCalls(t *testing.T) {
	m, address := simdMemory(t)
	for i := 0; i < 16*19; i++ {
		m[1+i] = byte(19 + i)
	}
	callback, err := foreign.RegisterCallback(func() uintptr { runtime.GC(); return 42 })
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 8, 13, 19} {
		for _, kind := range []string{"foreign", "managed-c", "managed-go"} {
			for _, entry := range []abi.Flavor{abi.C, abi.Go} {
				if kind == "foreign" && entry == abi.Go {
					continue
				}
				t.Run(fmt.Sprintf("%s/%d/entry%d", kind, n, entry), func(t *testing.T) {
					b := nyanjit.NewBuilder()
					p, out := b.ParamNative(), b.ParamNative()
					shape := ir.Vec(16, 8)
					values := make([]nyanjit.Value, n)
					for i := range values {
						values[i] = b.VLoad(shape, b.Add(p, b.ConstNative(uintptr(i*16))))
					}
					var called nyanjit.Value
					if kind == "managed-go" {
						called = b.CallGo(b.Const(uint64(reflect.ValueOf(simdGoCallback).Pointer())), b.Const(41))
					} else {
						called, err = foreign.Call(b, callback)
						if err != nil {
							t.Fatal(err)
						}
					}
					for i, v := range values {
						b.VStore(b.Add(out, b.ConstNative(uintptr(i*16))), b.Add(v, b.Splat(shape, b.LiteralBits(8, 1))))
					}
					b.Return(called)
					program, err := testFinishRaw(b)
					if err != nil {
						t.Fatal(err)
					}
					options := compile.Func{ABI: entry, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}, AssumePointerFree: true}
					if kind == "foreign" {
						options.Context = compile.ContextForeign
					}
					template, err := lowerTemplate(program, compile.TargetSpec{}, options)
					if err != nil {
						t.Fatalf("%s: %v", kind, err)
					}
					code, err := template.Bind(nil)
					if managedUnsupported(err) {
						t.Skip("Go-managed code needs goregister:", err)
					}
					if err != nil {
						t.Fatal(err)
					}
					got, err := code.Call(execution.Words(uintptr(address+1), uintptr(address+1025))...)
					if err != nil || got != 42 {
						t.Fatalf("%s result=%d,%v", kind, got, err)
					}
					for i := 0; i < 16*n; i++ {
						if m[1025+i] != m[1+i]+1 {
							t.Fatalf("%s vector lost at lane%d", kind, i)
						}
					}
					if template.Report().SpillStorage() != abi.SpillStack {
						t.Fatal("live SIMD was not scheduled to a call-safe home")
					}
					if kind == "managed-c" {
						listing, err := lowerListing(program, compile.TargetSpec{}, options)
						if err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(listing, "nyanjit") {
							t.Fatalf("managed C listing omitted its gateway:\n%s", listing)
						}
					}
					code.Close()
				})
			}
		}
	}
}
