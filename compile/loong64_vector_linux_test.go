//go:build linux && loong64

package compile_test

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/cpuprofile"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestLoong64MixedFPVectorPressureAndCalls(t *testing.T) {
	frameLimit := loong64.NewPolicy(abi.C, abi.StackPlatform).ManagedFrameLimit()
	m, ptr := simdMemory(t)
	for k := range 512 {
		m[k] = byte(k*37 + 13)
	}
	features := []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()}
	if cpuprofile.Host().Has("lsx") {
		features = append(features, compile.VectorsRequire("lsx"))
	}
	for _, feature := range features {
		for _, fpBits := range []uint8{32, 64} {
			for _, kind := range []string{"leaf", "foreign-C", "managed-C", "managed-Go"} {
				for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
					if kind != "leaf" && stack == abi.StackNoUse {
						continue
					}
					t.Run(fmt.Sprintf("features%v/fp%d/%s/stack%d", feature, fpBits, kind, stack), func(t *testing.T) {
						live, fpLive := 24, 12
						if kind != "leaf" {
							live, fpLive = 6, 6
						}
						b := n.NewBuilder()
						in, out, x := b.ParamNative(), b.ParamNative(), b.ParamBits(fpBits)
						vectors := make([]n.Value, live)
						for k := range vectors {
							vectors[k] = b.VLoad(ir.Vec(2, 64), b.Add(in, b.ConstNative(uintptr(k*16))))
						}
						floats := make([]n.Value, fpLive)
						var want uint64
						for k := range floats {
							floats[k] = b.FAdd(x, fpConstant(b, fpBits, float64(k+1)))
							b.StoreBits(b.Add(out, b.ConstNative(uintptr(512+k*8))), floats[k])
							want ^= fpBitsValue(fpBits, float64(k+3))
						}
						result := b.Const(0)
						if kind == "managed-Go" {
							result = b.CallGo(b.ConstNative(reflect.ValueOf(fpGrowAndGC).Pointer()), b.Const(42))
							want ^= 42
						} else if kind != "leaf" {
							// Destroy all volatile scalar FP values, and all 128 bits of
							// their vector aliases on LSX hosts, using a valid LP64D leaf.
							a := loong64.NewByteAssembler(abi.StackNoUse, 0)
							for r := uint8(0); r < 24; r++ {
								if cpuprofile.Host().Has("lsx") {
									if err := a.Vector(machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVZero, Bits: 8, A: backend.VectorRegister(r)}); err != nil {
										t.Fatal(err)
									}
								} else {
									a.Float(machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: backend.VectorRegister(r), B: 0})
								}
							}
							a.Constant(4, 42)
							a.Ret()
							callee, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C}, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer callee.Close()
							entry, err := callee.EntryAddress()
							if err != nil {
								t.Fatal(err)
							}
							result = b.CallC(b.ConstNative(entry))
							want ^= 42
						}
						for k, v := range vectors {
							b.VStore(b.Add(out, b.ConstNative(uintptr(16*k))), v)
						}
						for _, v := range floats {
							result = b.Xor(result, b.CastUnsigned(v, 64))
						}
						b.Return(result)
						p, err := testFinishRaw(b)
						if err != nil {
							t.Fatal(err)
						}
						spec := compile.TargetSpec{Vectors: feature}
						fn := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: stack}, AssumePointerFree: true}
						if stack == abi.StackPlatform {
							fn.Stack.BytesAvailable = 4096
						}
						if kind == "foreign-C" {
							fn.Context = compile.ContextForeign
						} else if kind != "leaf" {
							fn.Context = compile.ContextGoManaged
						}
						template, err := lowerTemplate(p, spec, fn)
						if err != nil {
							t.Fatal(err)
						}
						if kind != "leaf" && kind != "foreign-C" {
							// Use the same lowering as the template's frame-cap check,
							// including entry preservation.
							report, err := lowerReport(p, spec, fn)
							if err != nil {
								t.Fatal(err)
							}
							if limit := frameLimit; report.StackBytes() > limit {
								t.Fatalf("managed frame exceeds %d: %d", limit, report.StackBytes())
							}
						}
						c, err := template.Bind(nil)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						got, err := c.Call(execution.Words(ptr, ptr+1024, uintptr(fpBitsValue(fpBits, 2)))...)
						if err != nil || uint64(got) != want {
							t.Fatalf("FP/call result=%x,%v want%x", got, err, want)
						}
						if !bytes.Equal(m[:live*16], m[1024:1024+live*16]) {
							t.Fatal("vector spill truncated or collided with FP home")
						}
						if stack == abi.StackNoUse && template.Report().WorkspaceBytes() < 16 || stack == abi.StackPlatform && template.Report().StackBytes() < 16 {
							t.Fatal("pressure did not spill")
						}
						if stack == abi.StackNoUse && loong64VectorBits(t, spec) == 128 && template.Report().WorkspaceAlignment() != 16 {
							t.Fatal("vector workspace is not aligned")
						}
					})
				}
			}
		}
	}
}

func fpBitsValue(w uint8, x float64) uint64 {
	if w == 32 {
		return uint64(math.Float32bits(float32(x)))
	}
	return math.Float64bits(x)
}

func TestLoong64LSXAliasedDestinations(t *testing.T) {
	if !cpuprofile.Host().Has("lsx") {
		t.Skip("host has no LSX")
	}
	f := backend.VectorRegister
	m, ptr := simdMemory(t)
	for _, bits := range []uint8{8, 16, 32, 64} {
		for _, operation := range []string{"add", "sub", "mul", "and", "or", "xor", "shl", "shr", "sar", "rol", "ror"} {
			for _, dst := range []uint8{8, 9, 10} {
				for _, scalar := range []bool{false, true} {
					arithmetic := map[string]ir.Opcode{"add": ir.OpAdd, "sub": ir.OpSub, "mul": ir.OpMul, "and": ir.OpAnd, "or": ir.OpOr, "xor": ir.OpXor, "shl": ir.OpShiftLeftBy, "shr": ir.OpShiftRightBy, "sar": ir.OpShiftRightArithmeticBy, "rol": ir.OpRotateLeftBy, "ror": ir.OpRotateRightBy}[operation]
					_, by := op.ShiftByOperation(arithmetic)
					if scalar && !by {
						continue
					}
					a := loong64.NewByteAssembler(abi.StackNoUse, 0)
					for _, i := range []machine.Instruction{
						{Op: machine.OpVector, Arithmetic: ir.OpVLoad, Bits: bits, A: f(8), B: 4},
						{Op: machine.OpVector, Arithmetic: ir.OpVLoad, Bits: bits, A: f(9), B: 4, Disp: 16},
					} {
						if err := a.Vector(i); err != nil {
							t.Fatal(err)
						}
					}
					i := machine.Instruction{Op: machine.OpVector, Arithmetic: arithmetic, Bits: bits, A: f(dst), B: f(8), C: f(9)}
					if scalar {
						i.C = 6
					}
					if err := a.Vector(i); err != nil {
						t.Fatal(err)
					}
					if err := a.Vector(machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVStore, Bits: bits, A: 5, B: f(dst)}); err != nil {
						t.Fatal(err)
					}
					a.Constant(4, 0)
					a.Ret()
					c, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 3}, nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, count := range []uint64{0, 1, uint64(bits - 1), uint64(bits), uint64(bits + 1), 255, 256, 1 << 63, ^uint64(0)} {
						want := make([]byte, 16)
						for lane := 0; lane < 128/int(bits); lane++ {
							step := int(bits / 8)
							x := uint64(0x8123456789abcdef) + uint64(lane)*0x1313131313131313
							y := count + uint64(lane)
							writeSIMDLane(m[lane*step:], bits, x)
							writeSIMDLane(m[16+lane*step:], bits, y)
							n := readSIMDLane(m[16+lane*step:], bits)
							if scalar {
								n = count
							}
							writeSIMDLane(want[lane*step:], bits, scalarSIMDResult(operation, bits, x, readSIMDLane(m[16+lane*step:], bits), n))
						}
						if _, err := c.Call(execution.Words(ptr, ptr+64, uintptr(count))...); err != nil {
							t.Fatal(err)
						}
						if !slices.Equal(m[64:80], want) {
							t.Fatalf("%s bits%d dst%d scalar%v count%x got%x want%x", operation, bits, dst, scalar, count, m[64:80], want)
						}
					}
					c.Close()
				}
			}
		}
	}
}

func TestLoong64LSXManagedFrameBoundary(t *testing.T) {
	frameLimit := loong64.NewPolicy(abi.C, abi.StackPlatform).ManagedFrameLimit()
	if !cpuprofile.Host().Has("lsx") {
		t.Skip("host has no LSX")
	}
	foundLimit, foundAbove := false, false
	mem, ptr := simdMemory(t)
	for k := range 768 {
		mem[k] = byte(k*29 + 7)
	}
	for live := 1; live <= 48; live++ {
		b := n.NewBuilder()
		in, out := b.ParamNative(), b.ParamNative()
		values := make([]n.Value, live)
		for k := range values {
			values[k] = b.VLoad(ir.Vec(4, 32), b.Add(in, b.ConstNative(uintptr(16*k))))
		}
		called := b.CallGo(b.ConstNative(reflect.ValueOf(fpGrowAndGC).Pointer()), b.Const(42))
		for k, v := range values {
			b.VStore(b.Add(out, b.ConstNative(uintptr(16*k))), v)
		}
		b.Return(called)
		p, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		spec := compile.TargetSpec{Vectors: compile.VectorsRequire("lsx")}
		fn := compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true}
		// The lowering report gives the frame size without the managed-frame
		// limit check that encoding a template applies.
		report, err := lowerReport(p, spec, fn)
		if err != nil {
			t.Fatal(err)
		}
		size := report.StackBytes()
		if size < frameLimit {
			continue
		}
		template, err := lowerTemplate(p, spec, fn)
		if size == frameLimit {
			if err != nil {
				t.Fatalf("%d-byte vector frame: %v", frameLimit, err)
			}
			foundLimit = true
			code, err := template.Bind(nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := code.Call(execution.Words(ptr, ptr+1024)...)
			code.Close()
			if err != nil || got != 42 || !bytes.Equal(mem[:live*16], mem[1024:1024+live*16]) {
				t.Fatalf("%d-byte vector frame lost live data across stack growth/GC: %d,%v", frameLimit, got, err)
			}
			t.Logf("%d live vectors: %d-byte managed frame accepted", live, frameLimit)
		} else {
			if size != frameLimit+16 || !errors.Is(err, compile.ErrBackendLimit) {
				t.Fatalf("frame%d error%v want%d/ErrBackendLimit", size, err, frameLimit+16)
			}
			foundAbove = true
			break
		}
	}
	if !foundLimit || !foundAbove {
		t.Fatalf("missing vector frame boundaries: limit%v above%v", foundLimit, foundAbove)
	}
}

func TestLoong64LSXIndexedMemory(t *testing.T) {
	if !cpuprofile.Host().Has("lsx") {
		t.Skip("host has no LSX")
	}
	m, ptr := simdMemory(t)
	for k := range 512 {
		m[k] = byte(k*17 + 23)
	}
	b := n.NewBuilder()
	in, out, index := b.ParamNative(), b.ParamNative(), b.ParamNative()
	b.VStore(b.Add(out, index), b.VLoad(ir.Vec(16, 8), b.Add(in, index)))
	b.Return(b.ConstNative(0))
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	c, err := n.Compile(p, compile.TargetSpec{Vectors: compile.VectorsRequire("lsx")}, compile.Func{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, index := range []uintptr{0, 1, 7, 15, 31, 127} {
		for k := 2048; k < 2560; k++ {
			m[k] = 0xa5
		}
		if _, err := c.Call(execution.Words(ptr, ptr+2048, index)...); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(m[index:index+16], m[2048+index:2048+index+16]) {
			t.Fatalf("indexed vector load/store at %d lost bytes", index)
		}
		if index > 0 && m[2048+index-1] != 0xa5 || m[2048+index+16] != 0xa5 {
			t.Fatal("indexed vector store overwrote guard")
		}
	}
}

// loong64VectorBits reports the preferred SIMD width that spec resolves to.
func loong64VectorBits(t *testing.T, spec compile.TargetSpec) int {
	t.Helper()
	target, err := compile.NewTarget(spec)
	if err != nil {
		t.Fatal(err)
	}
	return target.Vectors().PreferredBits
}

// Check the fixed 2x64 vector-call boundary using the complete managed emission.
func TestLoong64LSXManagedVectorCallFrameBoundary(t *testing.T) {
	frameLimit := loong64.NewPolicy(abi.C, abi.StackPlatform).ManagedFrameLimit()
	if !cpuprofile.Host().Has("lsx") {
		t.Skip("host has no LSX")
	}
	for _, live := range []int{22, 23} {
		b := n.NewBuilder()
		in, out := b.ParamBits(64), b.ParamBits(64)
		values := make([]n.Value, live)
		for k := range values {
			values[k] = b.VLoad(ir.Vec(2, 64), b.Add(in, b.Const(uint64(16*k))))
		}
		called := b.CallGo(b.Const(0x1234), b.Const(42))
		for k, v := range values {
			b.VStore(b.Add(out, b.Const(uint64(16*k))), v)
		}
		b.Return(called)
		p, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		spec := compile.TargetSpec{Architecture: "loong64", Vectors: compile.VectorsRequire("lsx")}
		fn := compile.Func{ABI: abi.C, Context: compile.ContextGoManaged, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}, AssumePointerFree: true}
		report, err := lowerReport(p, spec, fn)
		if err != nil {
			t.Fatal(err)
		}
		_, err = lowerTemplate(p, spec, fn)
		t.Logf("live%d: complete frame%d prepare%v", live, report.StackBytes(), err)
		if want := frameLimit + (live-22)*16; report.StackBytes() != want {
			t.Fatalf("live%d: frame%d want%d", live, report.StackBytes(), want)
		}
		if live == 22 {
			if report.StackBytes() > frameLimit || err != nil {
				t.Fatalf("22 vectors should fit: frame%d err%v", report.StackBytes(), err)
			}
		} else if report.StackBytes() <= frameLimit || !errors.Is(err, compile.ErrBackendLimit) {
			t.Fatalf("23 vectors should exceed cap: frame%d err%v", report.StackBytes(), err)
		}
	}
}
