//go:build linux && 386

package compile_test

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func Test386MixedFPVectorPressure(t *testing.T) {
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		for _, w := range []uint8{32, 64} {
			for _, call := range []bool{false, true} {
				if call && stack == abi.StackNoUse {
					continue
				} // Existing call contract requires a platform frame.
				t.Run(fmt.Sprintf("stack%d/FP%d/call%v", stack, w, call), func(t *testing.T) {
					m, pointer := simdMemory(t)
					for k := 0; k < 256; k++ {
						m[1+k] = byte(k*37 + 9)
					}
					b := n.NewBuilder()
					in, out, x := b.ParamNative(), b.ParamNative(), b.ParamBits(w)
					vectorCount, floatCount := 16, 12
					if call {
						vectorCount, floatCount = 6, 6
					} // Keep managed frames within the existing 240-byte limit.
					vectors := make([]n.Value, vectorCount)
					for k := range vectors {
						vectors[k] = b.VLoad(ir.Vec(2, 64), b.Add(in, b.ConstNative(uintptr(k*16))))
					}
					floats := make([]n.Value, floatCount)
					for k := range floats {
						floats[k] = b.FAdd(x, fpConstant(b, w, float64(k+1)))
					}
					// Force computations before the call/vector stores, retaining their FP
					// values for the sum afterward. The stores are observable scheduling barriers.
					for k, v := range floats {
						b.StoreBits(b.Add(out, b.ConstNative(uintptr(512+k*8))), v)
					}
					var result n.Value
					if call {
						result = b.CallGo(b.ConstNative(reflect.ValueOf(fpGrowAndGC).Pointer()), b.Const(42))
					}
					for k, v := range vectors {
						b.VStore(b.Add(out, b.ConstNative(uintptr(k*16))), v)
					}
					sum := floats[0]
					for _, v := range floats[1:] {
						sum = b.FAdd(sum, v)
					}
					sum = b.CastUnsigned(sum, 64)
					if call {
						sum = b.Xor(sum, result)
					}
					b.Return(sum)
					program, err := testFinishRaw(b)
					if err != nil {
						t.Fatal(err)
					}
					opts := compile.Func{Stack: compile.StackPolicy{Practice: stack}, AssumePointerFree: call}
					if stack == abi.StackPlatform {
						opts.Stack.BytesAvailable = 8192
					}
					if call {
						opts.Context = compile.ContextGoManaged
					} else if stack == abi.StackPlatform {
						opts.Context = compile.ContextForeign
					}
					template, err := lowerTemplate(program, compile.TargetSpec{}, opts)
					if err != nil {
						t.Fatal(err)
					}
					code, err := template.Bind(nil)
					if err != nil {
						t.Fatal(err)
					}
					defer code.Close()
					args := []any{pointer + 1, pointer + 1025}
					if w == 64 {
						args = append(args, float64(2))
					} else {
						args = append(args, uintptr(math.Float32bits(2)))
					}
					lo, hi, err := code.CallPair(execution.Words(args...)...)
					want := fpBits(w, float64(2*floatCount+floatCount*(floatCount+1)/2))
					if call {
						want ^= 42
					}
					if err != nil || execution.Wide(lo, hi) != want {
						t.Fatalf("FP value got%x:%x,%v want%x", hi, lo, err, want)
					}
					if !bytes.Equal(m[1:1+vectorCount*16], m[1025:1025+vectorCount*16]) {
						t.Fatal("vector/FP homes overlap or spill was truncated")
					}
					report := template.Report()
					if report.WorkspaceBytes() < 16 && stack == abi.StackNoUse || report.StackBytes() < 16 && stack == abi.StackPlatform {
						t.Fatalf("pressure did not spill vectors %+v", report.Snapshot())
					}
					if report.WorkspaceAlignment() != 16 {
						t.Fatalf("workspace alignment%d", report.WorkspaceAlignment())
					}
				})
			}
		}
	}
}

func Test386VectorsSurviveCClobbers(t *testing.T) {
	// Valid cdecl leaf: destroys all caller-saved XMMs, returns uint64(42).
	raw := []byte{}
	for r := byte(0); r < 8; r++ {
		raw = append(raw, 0x66, 0x0f, 0xef, 0xc0|r<<3|r)
	}
	raw = append(raw, 0xb8, 42, 0, 0, 0, 0x31, 0xd2, 0xc3)
	callee, err := execution.NewCode(raw, execution.CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer callee.Close()
	entry, err := callee.EntryAddress()
	if err != nil {
		t.Fatal(err)
	}
	m, pointer := simdMemory(t)
	for k := 0; k < 128; k++ {
		m[1+k] = byte(k*29 + 3)
	}
	b := n.NewBuilder()
	in, out := b.ParamNative(), b.ParamNative()
	vs := make([]n.Value, 8)
	for k := range vs {
		vs[k] = b.VLoad(ir.Vec(4, 32), b.Add(in, b.ConstNative(uintptr(k*16))))
	}
	called := b.CallC(b.ConstNative(entry))
	for k, v := range vs {
		b.VStore(b.Add(out, b.ConstNative(uintptr(k*16))), v)
	}
	b.Return(called)
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lo, hi, err := c.CallPair(pointer+1, pointer+1025)
	if err != nil || execution.Wide(lo, hi) != 42 || !bytes.Equal(m[1:129], m[1025:1153]) {
		t.Fatalf("live vectors lost across C call; result%x:%x err%v", hi, lo, err)
	}
}
