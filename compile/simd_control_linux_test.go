//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64 || ppc64le)

package compile_test

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"testing"
)

func TestSIMDTypedLabelsMasksLoopsAndScopes(t *testing.T) {
	m, address := simdMemory(t)
	for i := 0; i < 16; i++ {
		m[i] = byte(i)
	}
	b := nyanjit.NewBuilder()
	out, p, n := b.ParamNative(), b.ParamNative(), b.ParamNative()
	loop, done := b.NewLabel(), b.NewLabel()
	shape := nyanjit.Vec(16, 8)
	b.JumpArgs(loop, out, n, b.VLoad(shape, p), b.VCompare(b.VLoad(shape, p), nyanjit.BranchEqual, b.Splat(shape, b.LiteralBits(8, 5))).Value())
	state := b.BindParamsTypes(loop, nyanjit.Scalar(nyanjit.NativeBits), nyanjit.Scalar(nyanjit.NativeBits), shape, nyanjit.Mask(16))
	b.Enter()
	value := b.Add(state[2], b.Splat(shape, b.LiteralBits(8, 1)))
	b.Leave(value)
	next := b.Sub(state[1], b.ConstNative(1))
	b.BranchArgs(b.IsZero(next), done, []nyanjit.Value{state[0], value, state[3]}, loop, []nyanjit.Value{state[0], next, value, state[3]})
	result := b.BindParamsTypes(done, nyanjit.Scalar(nyanjit.NativeBits), shape, nyanjit.Mask(16))
	b.VStore(result[0], result[1])
	b.Return(b.MaskBits(result[2].Mask()))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, features := range [][]string{nil, {}} {
		code, err := compile.Compile(program, abi.CompileOptions{Features: features, Stack: abi.StackPlatform, StackBytesAvailable: 65536})
		if err != nil {
			t.Fatal(err)
		}
		lo, hi, err := code.CallPair(address+128, address, 3)
		if got := execution.Wide(lo, hi); err != nil || got != 32 {
			t.Fatalf("typed mask result %d,%v", got, err)
		}
		for i := 0; i < 16; i++ {
			if m[128+i] != byte(i+3) {
				t.Fatalf("loop lane%d=%d;want%d", i, m[128+i], i+3)
			}
		}
		code.Close()
	}
}
func TestSIMDPureLiteralSplatRematerializesAcrossLabels(t *testing.T) {
	b := nyanjit.NewBuilder()
	shape := nyanjit.Vec(4, 32)
	literal := b.Splat(shape, b.LiteralBits(32, 42))
	b.ExtractLane(literal, 0)
	next := b.NewLabel()
	b.Jump(next)
	b.Bind(next)
	b.Return(b.ExtractLane(literal, 3))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.Compile(program, abi.CompileOptions{Stack: abi.StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	lo, hi, err := code.CallPair()
	if got := execution.Wide(lo, hi); err != nil || got != 42 {
		t.Fatalf("literal rematerialization %d,%v", got, err)
	}
}
func TestSIMDRegisterPressureAndCompleteVectorSpills(t *testing.T) {
	m, address := simdMemory(t)
	for i := 0; i < 384; i++ {
		m[1+i] = byte(i)
	}
	b := nyanjit.NewBuilder()
	p, out := b.ParamNative(), b.ParamNative()
	shape := nyanjit.Vec(16, 8)
	values := make([]nyanjit.Value, 24)
	for i := range values {
		values[i] = b.VLoad(shape, b.Add(p, b.ConstNative(uintptr(i*16))))
	}
	result := values[0]
	for _, v := range values[1:] {
		result = b.Xor(result, v)
	}
	b.VStore(out, result)
	b.Return(b.Literal(0))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		budget := 0
		if stack == abi.StackPlatform {
			budget = 65536
		}
		template, err := compile.Prepare(program, abi.CompileOptions{Stack: stack, StackBytesAvailable: budget})
		if err != nil {
			t.Fatal(err)
		}
		code, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = code.Call(address+1, address+1025)
		if err != nil {
			t.Fatal(err)
		}
		for lane := 0; lane < 16; lane++ {
			want := byte(0)
			for v := 0; v < 24; v++ {
				want ^= m[1+v*16+lane]
			}
			if m[1025+lane] != want {
				t.Fatalf("spilled vector lane%d=%d;want%d", lane, m[1025+lane], want)
			}
		}
		if template.Report().SpillStorage == abi.SpillNone {
			t.Fatal("test did not exercise pressure")
		}
		code.Close()
	}
}

func TestSIMDPressureSubtractAndLaneReload(t *testing.T) {
	m, address := simdMemory(t)
	for _, width := range []uint8{8, 16, 32, 64} {
		shape := nyanjit.Vec(128/uint16(width), width)
		step := int(width / 8)
		for i := 0; i < 24; i++ {
			for n := 0; n < 16; n += step {
				writeSIMDLane(m[1+i*16+n:], width, uint64(i*379+n*17+5))
			}
		}
		b := nyanjit.NewBuilder()
		p, out := b.ParamNative(), b.ParamNative()
		values := make([]nyanjit.Value, 24)
		for i := range values {
			values[i] = b.VLoad(shape, b.Add(p, b.ConstNative(uintptr(i*16))))
		}
		result := values[0]
		for _, v := range values[1:] {
			result = b.Sub(v, result)
		}
		lane := b.ExtractLane(values[0], 0)
		result = b.InsertLane(result, shape.Lanes-1, lane)
		b.VStore(out, result)
		b.Return(b.CastUnsigned(lane, 64))
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		for _, features := range [][]string{nil, {}} {
			for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
				budget := 0
				if stack == abi.StackPlatform {
					budget = 65536
				}
				template, err := compile.Prepare(program, abi.CompileOptions{Features: features, Stack: stack, StackBytesAvailable: budget})
				if err != nil {
					t.Fatalf("width%d scalar=%v stack=%v: %v", width, features != nil, stack, err)
				}
				code, err := template.Bind(nil)
				if err != nil {
					t.Fatal(err)
				}
				lo, hi, err := code.CallPair(address+1, address+1025)
				if got := execution.Wide(lo, hi); err != nil || got != readSIMDLane(m[1:], width) {
					t.Fatalf("width%d extract got%#x,%v", width, got, err)
				}
				for n := 0; n < 16; n += step {
					want := readSIMDLane(m[1+n:], width)
					for i := 1; i < 24; i++ {
						want = scalarSIMDResult("sub", width, readSIMDLane(m[1+i*16+n:], width), want, 0)
					}
					if n == 16-step {
						want = readSIMDLane(m[1:], width)
					}
					if got := readSIMDLane(m[1025+n:], width); got != want {
						t.Fatalf("width%d lane%d got%#x want%#x", width, n/step, got, want)
					}
				}
				if template.Report().SpillStorage == abi.SpillNone {
					t.Fatal("pressure did not cause spills")
				}
				code.Close()
			}
		}
	}
}
