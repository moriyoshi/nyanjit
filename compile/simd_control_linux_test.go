//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSIMDTypedLabelsMasksLoopsAndScopes(t *testing.T) {
	m, address := simdMemory(t)
	for i := 0; i < 16; i++ {
		m[i] = byte(i)
	}
	b := nyanjit.NewBuilder()
	out, p, n := b.ParamNative(), b.ParamNative(), b.ParamNative()
	loop, done := b.NewLabel(), b.NewLabel()
	shape := ir.Vec(16, 8)
	// The vector and the mask live in Vars; out crosses by dominance.
	count, vector := b.Var(n), b.Var(b.VLoad(shape, p))
	mask := b.Var(b.VCompare(b.VLoad(shape, p), ir.BranchEqual, b.Splat(shape, b.LiteralBits(8, 5))).Value())
	b.Bind(loop)
	b.Enter()
	value := b.Add(b.Get(vector), b.Splat(shape, b.LiteralBits(8, 1)))
	b.Leave(value)
	next := b.Sub(b.Get(count), b.ConstNative(1))
	b.Branch(b.IsZero(next), done)
	b.Set(count, next)
	b.Set(vector, value)
	b.Jump(loop)
	b.Bind(done)
	b.VStore(out, value)
	b.Return(b.MaskBits(b.Get(mask).Mask()))
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
		code, err := nyanjit.Compile(program, compile.TargetSpec{Vectors: vectors}, compile.Func{Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
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

// The raw-IR form of TestSIMDTypedLabelsMasksLoopsAndScopes: the counter, a
// vector and a mask are block parameters of the loop and the exit.
func TestSIMDTypedBlockParametersLoop(t *testing.T) {
	m, address := simdMemory(t)
	for i := 0; i < 16; i++ {
		m[i] = byte(i)
	}
	r := newRawIR()
	pb := r.pb
	entry := pb.Entry()
	native := ir.Scalar(uint8(strconv.IntSize))
	shape, mask := ir.Vec(16, 8), ir.Mask(16)
	out, p, n := pb.Param(entry, native), pb.Param(entry, native), pb.Param(entry, native)
	splat := func(block ir.Block, x uint64) int {
		return pb.Append(block, ir.Instruction{Op: ir.OpVSplat, Type: shape, Bits: 8, A: r.constant(block, 8, x)})
	}
	vector := pb.Append(entry, ir.Instruction{Op: ir.OpVLoad, Type: shape, Bits: 8, A: p})
	lanes := pb.Append(entry, ir.Instruction{Op: ir.OpVCompare, Type: mask, Bits: 8, A: vector, B: splat(entry, 5), Imm: uint64(ir.BranchEqual)})
	loop, done := pb.NewBlock(native, shape, mask), pb.NewBlock(shape, mask)
	pb.Jump(entry, loop, n, vector, lanes)
	state := r.params(loop, 3)
	value := pb.Append(loop, ir.Instruction{Op: ir.OpAdd, Type: shape, Bits: 8, A: state[1], B: splat(loop, 1)})
	next := r.op(loop, ir.OpSub, uint8(strconv.IntSize), state[0], r.constant(loop, uint8(strconv.IntSize), 1))
	pb.Branch(loop, rawIsZero(next), done, []int{value, state[2]}, loop, []int{next, value, state[2]})
	result := r.params(done, 2)
	pb.Append(done, ir.Instruction{Op: ir.OpVStore, Type: shape, Bits: 8, A: out, B: result[0]})
	pb.Return(done, r.op(done, ir.OpVMaskBits, 16, result[1], 0))
	program := r.seal()
	for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
		code, err := nyanjit.Compile(program, compile.TargetSpec{Vectors: vectors}, compile.Func{Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
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
	shape := ir.Vec(4, 32)
	literal := b.Splat(shape, b.LiteralBits(32, 42))
	b.ExtractLane(literal, 0)
	next := b.NewLabel()
	b.Jump(next)
	b.Bind(next)
	b.Return(b.ExtractLane(literal, 3))
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
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
	shape := ir.Vec(16, 8)
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
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		budget := 0
		if stack == abi.StackPlatform {
			budget = 65536
		}
		options := compile.Func{Stack: compile.StackPolicy{Practice: stack, BytesAvailable: budget}}
		if stack == abi.StackPlatform {
			options.Context = compile.ContextForeign
		}
		template, err := lowerTemplate(program, compile.TargetSpec{}, options)
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
		if template.Report().SpillStorage() == abi.SpillNone {
			t.Fatal("test did not exercise pressure")
		}
		code.Close()
	}
}

func TestSIMDPressureSubtractAndLaneReload(t *testing.T) {
	m, address := simdMemory(t)
	for _, width := range []uint8{8, 16, 32, 64} {
		shape := ir.Vec(128/uint16(width), width)
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
		program, err := testFinishRaw(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
			for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
				budget := 0
				if stack == abi.StackPlatform {
					budget = 65536
				}
				options := compile.Func{Stack: compile.StackPolicy{Practice: stack, BytesAvailable: budget}}
				if stack == abi.StackPlatform {
					options.Context = compile.ContextForeign
				}
				template, err := lowerTemplate(program, compile.TargetSpec{Vectors: vectors}, options)
				if err != nil {
					t.Fatalf("width%d scalar=%v stack=%v: %v", width, vectors == compile.VectorsOff(), stack, err)
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
				if template.Report().SpillStorage() == abi.SpillNone {
					t.Fatal("pressure did not cause spills")
				}
				code.Close()
			}
		}
	}
}
