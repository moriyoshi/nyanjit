//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// vectorVarKernels keep vectors and masks in Vars across blocks, loops and
// scopes. Their parameters and results are scalars.
var vectorVarKernels = []struct {
	name  string
	build func(*nyanjit.Builder)
	args  [][]uint64
}{
	{"loop-v4x32", func(b *nyanjit.Builder) {
		n := b.Param()
		t := ir.Vec(4, 32)
		acc := b.Var(b.ConstVector(t, laneBytes(32, 1, 2, 3, 0x80000000)))
		i := b.Var(b.Const(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		lane := b.CastUnsigned(b.Get(i), 32)
		step := b.InsertLane(b.Splat(t, b.ConstBits(32, 3)), 2, lane)
		b.Set(acc, b.Xor(b.Mul(b.Get(acc), step), b.RotateLeft(b.Get(acc), 7)))
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(foldLanes(b, b.Get(acc), 4, 32))
	}, [][]uint64{{0}, {1}, {7}, {100}}},
	{"diamond-v2x64", func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		t := ir.Vec(2, 64)
		v := b.Var(b.InsertLane(b.Splat(t, x), 1, y))
		odd, join := b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(b.And(x, b.Const(1))).Not(), odd)
		b.Set(v, b.Add(b.Get(v), b.Splat(t, b.Const(0x0123456789abcdef))))
		b.Jump(join)
		b.Bind(odd)
		b.Set(v, b.ShiftRightBy(b.Get(v), b.Const(3)))
		b.Jump(join)
		b.Bind(join)
		w := b.Get(v)
		b.Return(b.Xor(b.ExtractLane(w, 0), b.RotateLeft(b.ExtractLane(w, 1), 13)))
	}, [][]uint64{{0, 0}, {1, 2}, {0xfedcba9876543210, 5}, {^uint64(0), 1 << 63}}},
	{"mask-loop-v16x8", func(b *nyanjit.Builder) {
		n, seed := b.Param(), b.Param()
		t := ir.Vec(16, 8)
		data := make([]byte, 16)
		for k := range data {
			data[k] = byte(17*k + 5)
		}
		v := b.Var(b.ConstVector(t, data))
		m := b.Var(b.VCompare(b.Get(v), ir.BranchAbove, b.Splat(t, b.ConstBits(8, 0x80))).Value())
		bits := b.Var(b.Const(0))
		i := b.Var(b.Const(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		b.Set(bits, b.Xor(b.RotateLeft(b.Get(bits), 5), b.CastUnsigned(b.MaskBits(b.Get(m).Mask()), 64)))
		s := b.Splat(t, b.CastUnsigned(b.Add(seed, b.Get(i)), 8))
		b.Set(v, b.Add(b.Get(v), s))
		b.Set(m, b.VCompare(b.Get(v), ir.BranchLess, s).Value())
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Xor(b.Get(bits), b.CastUnsigned(b.MaskBits(b.Get(m).Mask()), 64)))
	}, [][]uint64{{0, 0}, {1, 9}, {5, 200}, {33, 77}}},
	{"scoped-v8x16", func(b *nyanjit.Builder) {
		n := b.Param()
		t := ir.Vec(8, 16)
		outside := b.Splat(t, b.CastUnsigned(n, 16))
		total := b.Var(b.Const(0))
		b.Enter()
		v := b.Var(b.Xor(outside, b.ConstVector(t, laneBytes(16, 1, 2, 3, 4, 5, 6, 7, 8))))
		m := b.Var(b.VCompare(b.Get(v), ir.BranchEqual, outside).Value())
		i := b.Var(b.Const(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, b.And(n, b.Const(15))), done)
		b.Enter()
		inner := b.Var(b.ShiftLeft(b.Get(v), 1))
		b.Set(inner, b.Sub(b.Get(inner), outside))
		b.Set(v, b.Get(inner))
		b.Leave()
		b.Set(m, b.VCompare(b.Get(v), ir.BranchGreater, outside).Value())
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		result := b.Get(v)
		b.Set(total, b.Add(foldLanes(b, result, 8, 16), b.CastUnsigned(b.MaskBits(b.Get(m).Mask()), 64)))
		b.Leave(result)
		b.Return(b.Xor(b.Get(total), b.CastUnsigned(b.ExtractLane(result, 3), 64)))
	}, [][]uint64{{0}, {3}, {0x1234}, {0xffff}}},
	{"late-vector", func(b *nyanjit.Builder) {
		// Scalar blocks first; the first vector comes mid-block, before
		// a scalar Var of a new scope.
		n := b.Param()
		i, acc := b.Var(b.Const(0)), b.Var(b.Const(1))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		b.Set(acc, b.Add(b.Mul(b.Get(acc), b.Const(5)), b.Get(i)))
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		t := ir.Vec(4, 16)
		x := b.Add(b.Splat(t, b.CastUnsigned(b.Get(acc), 16)), b.ConstVector(t, laneBytes(16, 1, 2, 3, 4)))
		b.Enter()
		k := b.Var(b.ExtractLane(x, 1))
		w := b.Var(x)
		again, out := b.NewLabel(), b.NewLabel()
		b.Bind(again)
		b.Branch(b.CompareImmediate(b.Get(k), ir.BranchBelow, 3), out)
		b.Set(w, b.Xor(b.Get(w), b.ShiftLeft(b.Get(w), 3)))
		b.Set(k, b.ShiftRight(b.Get(k), 1))
		b.Jump(again)
		b.Bind(out)
		r := b.Add(foldLanes(b, b.Get(w), 4, 16), b.CastUnsigned(b.Get(k), 64))
		b.Leave(r)
		b.Return(r)
	}, [][]uint64{{0}, {1}, {9}, {40}}},
	{"mixed-entry", func(b *nyanjit.Builder) {
		// Scalar Vars around the block's first vector, and a vector Var
		// declared after a scalar one; vectors also cross blocks by
		// dominance.
		n := b.Param()
		a := b.Var(b.Const(7))
		t := ir.Vec(4, 32)
		v := b.Add(b.Splat(t, b.CastUnsigned(n, 32)), b.ConstVector(t, laneBytes(32, 10, 20, 30, 40)))
		c := b.Var(b.Add(b.CastUnsigned(b.ExtractLane(v, 1), 64), n))
		w := b.Var(b.Mul(v, v))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.CompareImmediate(b.Get(a), ir.BranchEqual, 0), done)
		b.Set(w, b.Add(b.Get(w), b.Xor(v, b.Splat(t, b.CastUnsigned(b.Get(c), 32)))))
		b.Set(c, b.Add(b.Get(c), b.CastUnsigned(b.ExtractLane(b.Get(w), 3), 64)))
		b.Set(a, b.Sub(b.Get(a), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Xor(b.Get(c), foldLanes(b, b.Add(b.Get(w), v), 4, 32)))
	}, [][]uint64{{0}, {1}, {0x12345678}}},
	{"scope-first-vector", func(b *nyanjit.Builder) {
		// The function's first vector precedes a scalar Var declared in
		// the same block.
		n := b.Param()
		b.Enter()
		t := ir.Vec(8, 8)
		v := b.Xor(b.Splat(t, b.CastUnsigned(n, 8)), b.ConstVector(t, laneBytes(8, 1, 2, 4, 8, 16, 32, 64, 128)))
		c := b.Var(b.Add(b.CastUnsigned(b.ExtractLane(v, 5), 64), n))
		i := b.Var(b.Const(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.CompareImmediate(b.Get(i), ir.BranchEqual, 8), done)
		b.Set(c, b.Add(b.ShiftLeft(b.Get(c), 1), b.CastUnsigned(b.ExtractLane(v, 7), 64)))
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		r := b.Get(c)
		b.Leave(r)
		b.Return(r)
	}, [][]uint64{{0}, {3}, {0xff}}},
	{"swap-v4x32", func(b *nyanjit.Builder) {
		n := b.Param()
		t := ir.Vec(4, 32)
		x := b.Var(b.Splat(t, b.CastUnsigned(n, 32)))
		y := b.Var(b.ConstVector(t, laneBytes(32, 9, 8, 7, 6)))
		i := b.Var(b.Const(0))
		loop, skip, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.CompareImmediate(b.Get(i), ir.BranchEqual, 12), done)
		old := b.Get(x)
		b.Set(x, b.Add(b.Get(y), b.Splat(t, b.CastUnsigned(b.Get(i), 32))))
		b.Branch(b.IsZero(b.And(b.Get(i), b.Const(2))), skip)
		b.Set(y, b.Xor(old, b.ShiftRightArithmetic(b.Get(y), 1)))
		b.Bind(skip)
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Xor(foldLanes(b, b.Get(x), 4, 32), b.ShiftLeft(foldLanes(b, b.Get(y), 4, 32), 1)))
	}, [][]uint64{{0}, {1}, {0xdeadbeef}}},
}

// laneBytes encodes lanes of the given width in memory order.
func laneBytes(bits int, lanes ...uint64) []byte {
	out := make([]byte, 0, len(lanes)*bits/8)
	for _, x := range lanes {
		var word [8]byte
		binary.LittleEndian.PutUint64(word[:], x)
		out = append(out, word[:bits/8]...)
	}
	return out
}

// foldLanes mixes every lane of v into one 64-bit scalar.
func foldLanes(b *nyanjit.Builder, v nyanjit.Value, lanes uint16, bits uint8) nyanjit.Value {
	r := b.Const(0)
	for k := range lanes {
		x := b.ExtractLane(v, k)
		if bits != 64 {
			x = b.CastUnsigned(x, 64)
		}
		r = b.Add(b.RotateLeft(r, 11), x)
	}
	return r
}

// callWords runs a compiled function and returns its 64-bit result.
func callWords(t *testing.T, u *compile.Unit, args []uint64) uint64 {
	t.Helper()
	template, err := u.Template()
	if err != nil {
		t.Fatal(err)
	}
	code, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	words := make([]any, len(args))
	for i, x := range args {
		words[i] = x
	}
	if unsafe.Sizeof(uintptr(0)) == 4 {
		lo, hi, err := code.CallPair(execution.Words(words...)...)
		if err != nil {
			t.Fatal(err)
		}
		return uint64(lo) | uint64(hi)<<32
	}
	r, err := code.Call(execution.Words(words...)...)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(r)
}

// TestBaselineVectorVars runs the kernels at both tiers, through Lower and
// through Builders bound to each tier's Target, and compares every result
// with the optimized tier's Lower.
func TestBaselineVectorVars(t *testing.T) {
	// 386 frames over Code.Call's limit fall back to the workspace.
	funcs := []compile.Func{
		{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}},
		{ABI: abi.C},
	}
	if unsafe.Sizeof(uintptr(0)) == 4 {
		funcs = funcs[1:]
	}
	targets := map[compile.Tier]compile.Target{}
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		targets[tier] = target
	}
	for _, k := range vectorVarKernels {
		for _, f := range funcs {
			b := nyanjit.NewBuilder()
			k.build(b)
			p, err := b.Finish()
			if err != nil {
				t.Fatalf("%s: Finish: %v", k.name, err)
			}
			reference, err := compile.Lower(targets[compile.TierOptimized], p, f)
			if err != nil {
				t.Fatalf("%s: optimized Lower: %v", k.name, err)
			}
			units := map[string]*compile.Unit{}
			for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
				name := []string{"optimized", "baseline"}[tier]
				if tier == compile.TierBaseline {
					u, err := compile.Lower(targets[tier], p, f)
					if err != nil {
						t.Fatalf("%s: baseline Lower: %v", k.name, err)
					}
					units["baseline Lower"] = u
				}
				bound := nyanjit.NewBuilderFor(targets[tier], f)
				k.build(bound)
				u, err := bound.Lower()
				if err != nil {
					t.Fatalf("%s: %s Builder: %v", k.name, name, err)
				}
				units[name+" Builder"] = u
			}
			if ops := units["baseline Builder"].Report().VectorOperations(); len(ops) == 0 {
				t.Errorf("%s: the baseline Builder unit reports no vector operations", k.name)
			}
			for _, args := range k.args {
				want := callWords(t, reference, args)
				for name, u := range units {
					if got := callWords(t, u, args); got != want {
						listing, _ := u.Listing()
						t.Errorf("%s %s stack=%v %v: got %#x, want %#x\n%s", k.name, name, f.Stack.Practice, args, got, want, listing)
					}
				}
			}
		}
	}
}

// randomVectorVars returns a generator of a random loop over two vector Vars
// and a mask Var of one vector type, after a scalar prefix that may branch.
func randomVectorVars(seed int64) func(b *nyanjit.Builder) {
	return func(b *nyanjit.Builder) {
		r := rand.New(rand.NewSource(seed))
		types := []ir.Type{ir.Vec(4, 32), ir.Vec(2, 64), ir.Vec(16, 8), ir.Vec(8, 16), ir.Vec(8, 32)}
		t := types[r.Intn(len(types))]
		n, x := b.Param(), b.Param()
		lane := func(v nyanjit.Value) nyanjit.Value {
			s := b.ExtractLane(v, uint16(r.Intn(int(t.Lanes))))
			if t.LaneBits != 64 {
				s = b.CastUnsigned(s, 64)
			}
			return s
		}
		scal := func(v nyanjit.Value) nyanjit.Value {
			if t.LaneBits != 64 {
				return b.CastUnsigned(v, t.LaneBits)
			}
			return v
		}
		pre := b.Var(b.Add(x, b.Const(3)))
		branched := r.Intn(2) == 0
		if branched {
			l1, l2 := b.NewLabel(), b.NewLabel()
			b.Branch(b.IsZero(x), l1)
			b.Set(pre, b.Mul(b.Get(pre), b.Const(7)))
			b.Jump(l2)
			b.Bind(l1)
			b.Jump(l2)
			b.Bind(l2)
		}
		entered := branched || r.Intn(2) == 0
		if entered {
			b.Enter()
		}
		base := b.Splat(t, scal(b.Get(pre)))
		vs := []nyanjit.Var{b.Var(base), b.Var(b.InsertLane(base, 0, scal(n)))}
		m := b.Var(b.VCompare(base, ir.BranchAbove, b.Get(vs[1])).Value())
		acc := b.Var(b.Const(0))
		i := b.Var(b.Const(0))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
		for k := 0; k < 1+r.Intn(6); k++ {
			a, c := vs[r.Intn(2)], vs[r.Intn(2)]
			var v nyanjit.Value
			switch r.Intn(7) {
			case 0:
				v = b.Add(b.Get(a), b.Get(c))
			case 1:
				v = b.Xor(b.Get(a), b.RotateLeft(b.Get(c), uint8(1+r.Intn(int(t.LaneBits)-1))))
			case 2:
				v = b.Mul(b.Get(a), b.Splat(t, scal(b.Get(i))))
			case 3:
				v = b.InsertLane(b.Get(a), uint16(r.Intn(int(t.Lanes))), scal(lane(b.Get(c))))
			case 4:
				v = b.ShiftRightBy(b.Get(a), scal(b.And(b.Get(i), b.Const(7))))
			case 5:
				b.Set(m, b.VCompare(b.Get(a), []ir.BranchCondition{ir.BranchLess, ir.BranchEqual, ir.BranchAboveEqual}[r.Intn(3)], b.Get(c)).Value())
				continue
			case 6:
				skip := b.NewLabel()
				b.Branch(b.IsZero(b.And(b.Get(i), b.Const(1))), skip)
				b.Set(a, b.Sub(b.Get(a), b.Get(c)))
				b.Bind(skip)
				continue
			}
			b.Set(a, v)
		}
		b.Set(acc, b.Add(b.RotateLeft(b.Get(acc), 9), b.Add(lane(b.Get(vs[0])), b.CastUnsigned(b.MaskBits(b.Get(m).Mask()), 64))))
		b.Set(i, b.Add(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		res := b.Xor(b.Get(acc), b.Add(foldLanes(b, b.Get(vs[0]), t.Lanes, t.LaneBits), foldLanes(b, b.Get(vs[1]), t.Lanes, t.LaneBits)))
		if entered {
			b.Leave(res)
		}
		b.Return(res)
	}
}

// TestBaselineVectorVarSweep compares random vector Var programs at both
// tiers, through Lower and through a bound Builder.
func TestBaselineVectorVarSweep(t *testing.T) {
	f := compile.Func{ABI: abi.C}
	optimized, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
	if err != nil {
		t.Fatal(err)
	}
	for seed := int64(0); seed < 64; seed++ {
		build := randomVectorVars(seed)
		b := nyanjit.NewBuilder()
		build(b)
		p, err := b.Finish()
		if err != nil {
			t.Fatalf("seed %d: Finish: %v", seed, err)
		}
		reference, err := compile.Lower(optimized, p, f)
		if err != nil {
			t.Fatalf("seed %d: optimized Lower: %v", seed, err)
		}
		lowered, err := compile.Lower(baseline, p, f)
		if err != nil {
			t.Fatalf("seed %d: baseline Lower: %v", seed, err)
		}
		bound := nyanjit.NewBuilderFor(baseline, f)
		build(bound)
		streamed, err := bound.Lower()
		if err != nil {
			t.Fatalf("seed %d: baseline Builder: %v", seed, err)
		}
		for _, args := range [][]uint64{{0, 1}, {1, 0}, {5, 0x123456789}, {13, ^uint64(0)}} {
			want := callWords(t, reference, args)
			if got := callWords(t, lowered, args); got != want {
				t.Errorf("seed %d %v: baseline Lower %#x, want %#x", seed, args, got, want)
			}
			if got := callWords(t, streamed, args); got != want {
				t.Errorf("seed %d %v: baseline Builder %#x, want %#x", seed, args, got, want)
			}
		}
	}
}

// TestBaselineStreamVectorErrors checks what a baseline Stream rejects in
// the vectors it scalarizes.
func TestBaselineStreamVectorErrors(t *testing.T) {
	target, err := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
	if err != nil {
		t.Fatal(err)
	}
	v4 := ir.Vec(4, 32)
	splat := func(id int) ir.Instruction { return ir.Instruction{Op: ir.OpVSplat, Type: v4, Bits: 32, A: id} }
	for _, c := range []struct {
		name string
		code []ir.Instruction
		vars []ir.Variable
		step ir.ControlStep
		want error
	}{
		{"scalar use of a vector", []ir.Instruction{{Op: ir.OpParam, Bits: 32}, splat(0), {Op: ir.OpAdd, Bits: 32, A: 1, B: 0}},
			nil, ir.ControlStep{Term: ir.TermReturn, Result: 2}, compile.ErrInvalidFunction},
		{"vector result", []ir.Instruction{{Op: ir.OpParam, Bits: 32}, splat(0)},
			nil, ir.ControlStep{Term: ir.TermReturn, Result: 1}, compile.ErrInvalidFunction},
		{"splat of a mismatched lane", []ir.Instruction{{Op: ir.OpParam, Bits: 64}, splat(0), {Op: ir.OpVExtract, Bits: 32, A: 1}},
			nil, ir.ControlStep{Term: ir.TermReturn, Result: 2}, compile.ErrInvalidFunction},
		{"operand out of range", []ir.Instruction{{Op: ir.OpParam, Bits: 32}, splat(5)},
			nil, ir.ControlStep{Term: ir.TermReturn, Result: 0}, compile.ErrInvalidFunction},
		{"Set of a scalar into a vector Var", []ir.Instruction{{Op: ir.OpParam, Bits: 32}, splat(0), {Op: ir.OpSet, Type: v4, Bits: 32, A: 0}},
			[]ir.Variable{{Type: v4, Declaration: 2, Initial: []int{1}}}, ir.ControlStep{Term: ir.TermReturn, Result: 0}, compile.ErrInvalidFunction},
		{"vector edge argument", []ir.Instruction{{Op: ir.OpParam, Bits: 32}, splat(0)},
			nil, ir.ControlStep{Term: ir.TermJump, To: 0, Args: []int{1}}, compile.ErrUnsupported},
	} {
		s, err := compile.NewStream(target, compile.Func{ABI: abi.C})
		if err != nil {
			t.Fatal(err)
		}
		step := c.step
		step.End = len(c.code)
		err = s.Segment(&ir.Segment{Code: c.code, Variables: c.vars, Step: step})
		if err == nil {
			_, err = s.Unit()
		}
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}
