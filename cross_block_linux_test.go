//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64)

package nyanjit_test

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// crossBlockFuncs are the function shapes each cross-block program runs as.
var crossBlockFuncs = map[string]compile.Func{
	"plain":    {ABI: abi.C},
	"hardened": {ABI: abi.C, Harden: true},
	"stack":    {ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}},
}

func checkCrossBlock(t *testing.T, build func(b *nyanjit.Builder), want func(x, y uint64) uint64) {
	t.Helper()
	b := nyanjit.NewBuilder()
	build(b)
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	checkCrossBlockProgram(t, program, want)
}

func checkCrossBlockProgram(t *testing.T, program ir.Input, want func(x, y uint64) uint64) {
	t.Helper()
	inputs := [][2]uint64{{0, 0}, {1, 2}, {7, 5}, {5, 7}, {100, 3}, {^uint64(0), 9}}
	for name, f := range crossBlockFuncs {
		code, err := nyanjit.Compile(program, compile.TargetSpec{}, f)
		if name == "hardened" && errors.Is(err, compile.ErrUnsupported) {
			continue // some targets cannot harden control flow
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, in := range inputs {
			got, err := code.Call(uintptr(in[0]), uintptr(in[1]))
			if err != nil || uint64(got) != want(in[0], in[1]) {
				t.Errorf("%s: f(%d, %d) = %d, %v; want %d", name, in[0], in[1], got, err, want(in[0], in[1]))
			}
		}
		code.Close()
	}
}

func TestCrossBlockValues(t *testing.T) {
	t.Run("max without label arguments", func(t *testing.T) {
		checkCrossBlock(t, func(b *nyanjit.Builder) {
			x, y := b.Param(), b.Param()
			pickX, pickY := b.NewLabel(), b.NewLabel()
			b.Branch(b.Compare(x, ir.BranchAbove, y), pickX)
			b.Jump(pickY)
			b.Bind(pickX)
			b.Return(x)
			b.Bind(pickY)
			b.Return(y)
		}, func(x, y uint64) uint64 { return max(x, y) })
	})
	t.Run("diamond mixing a Var and outer values", func(t *testing.T) {
		checkCrossBlock(t, func(b *nyanjit.Builder) {
			x, y := b.Param(), b.Param()
			t3 := b.Mul(x, b.Const(3))
			merged := b.Var(b.Const(0))
			left, right, join := b.NewLabel(), b.NewLabel(), b.NewLabel()
			b.Branch(b.IsZero(y), left)
			b.Jump(right)
			b.Bind(left)
			b.Set(merged, b.Add(t3, b.Const(1)))
			b.Jump(join)
			b.Bind(right)
			b.Set(merged, b.Xor(t3, y))
			b.Jump(join)
			b.Bind(join)
			p := b.Get(merged)
			b.Return(b.Add(b.Add(p, t3), x))
		}, func(x, y uint64) uint64 {
			t3 := x * 3
			p := t3 ^ y
			if y == 0 {
				p = t3 + 1
			}
			return p + t3 + x
		})
	})
	t.Run("loop reading outer values", func(t *testing.T) {
		checkCrossBlock(t, func(b *nyanjit.Builder) {
			x, y := b.Param(), b.Param()
			var outer []nyanjit.Value
			for k := range 12 {
				outer = append(outer, b.Add(b.Mul(x, b.Const(uint64(k+1))), y))
			}
			total, count := b.Var(b.Const(0)), b.Var(b.Const(10))
			loop, done := b.NewLabel(), b.NewLabel()
			b.Bind(loop)
			acc := b.Get(total)
			for _, v := range outer {
				acc = b.Add(acc, v)
			}
			b.Set(total, acc)
			left := b.Sub(b.Get(count), b.Const(1))
			b.Set(count, left)
			b.Branch(b.IsZero(left), done)
			b.Jump(loop)
			b.Bind(done)
			b.Return(b.Add(b.Get(total), outer[0]))
		}, func(x, y uint64) uint64 {
			var sum uint64
			for k := range 12 {
				sum += x*uint64(k+1) + y
			}
			return 10*sum + x + y
		})
	})
	t.Run("value used two blocks later", func(t *testing.T) {
		checkCrossBlock(t, func(b *nyanjit.Builder) {
			x, y := b.Param(), b.Param()
			sum := b.Add(x, y)
			first, second := b.NewLabel(), b.NewLabel()
			b.Jump(first)
			b.Bind(first)
			diff := b.Sub(x, y)
			b.Jump(second)
			b.Bind(second)
			b.Return(b.Xor(sum, diff))
		}, func(x, y uint64) uint64 { return (x + y) ^ (x - y) })
	})
	t.Run("irreducible loop", func(t *testing.T) {
		// Two entries into the cycle a <-> c: the entry branches to either.
		checkCrossBlock(t, func(b *nyanjit.Builder) {
			x, y := b.Param(), b.Param()
			step := b.Add(y, b.Const(1))
			state := b.Var(b.Const(0))
			a, c, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
			b.Branch(b.Compare(x, ir.BranchAbove, y), a)
			b.Set(state, b.Const(1))
			b.Jump(c)
			b.Bind(a)
			b.Set(state, b.Add(b.Get(state), step))
			b.Jump(c)
			b.Bind(c)
			m := b.Get(state)
			b.Set(state, b.Add(m, x))
			b.Branch(b.Compare(m, ir.BranchAbove, b.Const(40)), done)
			b.Jump(a)
			b.Bind(done)
			b.Return(b.Add(m, step))
		}, func(x, y uint64) uint64 {
			step := y + 1
			var m uint64
			if x > y {
				m = step
			} else {
				m = 1
			}
			for m <= 40 {
				m = m + x + step
			}
			return m + step
		})
	})
	t.Run("unreachable block", func(t *testing.T) {
		checkCrossBlock(t, func(b *nyanjit.Builder) {
			x, y := b.Param(), b.Param()
			sum := b.Add(x, y)
			join, dead := b.NewLabel(), b.NewLabel()
			b.Jump(join)
			b.Bind(dead) // nothing jumps here
			b.Jump(join)
			b.Bind(join)
			b.Return(b.Mul(sum, x))
		}, func(x, y uint64) uint64 { return (x + y) * x })
	})
	t.Run("nested scope", func(t *testing.T) {
		checkCrossBlock(t, func(b *nyanjit.Builder) {
			x, y := b.Param(), b.Param()
			b.Enter()
			inner := b.Add(x, y)
			next := b.NewLabel()
			b.Jump(next)
			b.Bind(next)
			scaled := b.Mul(inner, b.Const(5))
			b.Leave(scaled)
			b.Return(b.Sub(scaled, x))
		}, func(x, y uint64) uint64 { return (x+y)*5 - x })
	})
}

func TestCrossBlockNonDominatingUse(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.Param()
	left, right, join := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Branch(b.IsZero(x), left)
	b.Jump(right)
	b.Bind(left)
	y := b.Add(x, b.Const(1))
	b.Jump(join)
	b.Bind(right)
	b.Jump(join)
	b.Bind(join)
	b.Return(y)
	_, err := b.Finish()
	var invalid *ir.ValidationError
	if !errors.As(err, &invalid) || !errors.Is(err, compile.ErrInvalidFunction) {
		t.Fatalf("Finish error = %v, want an ir.ValidationError and ErrInvalidFunction", err)
	}
}

func TestCrossBlockVectorsAndMasks(t *testing.T) {
	b := nyanjit.NewBuilder()
	address := b.Param()
	v := b.VLoad(ir.Vec(4, 32), address)
	mask := b.VCompare(v, ir.BranchAbove, b.Splat(ir.Vec(4, 32), b.ConstBits(32, 10)))
	join, dead := b.NewLabel(), b.NewLabel()
	b.Jump(join)
	b.Bind(dead) // unreachable: its edge passes zero vector and mask values
	b.Jump(join)
	b.Bind(join)
	b.VStore(b.Add(address, b.Const(16)), v)
	b.Return(b.MaskBits(mask))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range crossBlockFuncs {
		code, err := nyanjit.Compile(program, compile.TargetSpec{}, f)
		if name == "hardened" && errors.Is(err, compile.ErrUnsupported) {
			continue // some targets cannot harden control flow
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		buffer := [8]uint32{3, 11, 10, 400}
		got, err := code.Call(uintptr(unsafe.Pointer(&buffer[0])))
		code.Close()
		if err != nil || got != 0b1010 || buffer[4] != 3 || buffer[7] != 400 {
			t.Fatalf("%s: mask bits %b, %v; buffer %v", name, got, err, buffer)
		}
	}
}

func TestCrossBlockWithVars(t *testing.T) {
	checkCrossBlock(t, func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		scale := b.Add(y, b.Const(2))
		acc, i := b.Var(b.Const(0)), b.Var(b.And(x, b.Const(63)))
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(i)), done)
		b.Set(acc, b.Add(b.Get(acc), b.Mul(b.Get(i), scale)))
		b.Set(i, b.Sub(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Add(b.Get(acc), scale))
	}, func(x, y uint64) uint64 {
		scale, acc := y+2, uint64(0)
		for i := x & 63; i != 0; i-- {
			acc += i * scale
		}
		return acc + scale
	})
}

// TestCrossBlockBlockParameters runs the edge-argument shapes as raw IR, which
// keeps block parameters: a diamond whose join takes one parameter, a loop
// whose header takes its state, an irreducible cycle entered with different
// arguments, and a branch whose edges permute the parameters.
func TestCrossBlockBlockParameters(t *testing.T) {
	u64 := ir.Scalar(64)
	compare := func(a int, condition ir.BranchCondition, other int) *ir.PredicateExpr {
		return &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: condition, Value: a, Other: other, WithValue: true}
	}
	isZero := func(a int) *ir.PredicateExpr {
		return &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: a}
	}
	constant := func(pb *ir.ProgramBuilder, b ir.Block, v uint64) int {
		return pb.Append(b, ir.Instruction{Op: ir.OpConst, Imm: v})
	}
	cases := map[string]struct {
		build func(pb *ir.ProgramBuilder)
		want  func(x, y uint64) uint64
	}{
		"diamond": {func(pb *ir.ProgramBuilder) {
			entry := pb.Entry()
			x, y := pb.Param(entry, u64), pb.Param(entry, u64)
			t3 := pb.Append(entry, ir.Instruction{Op: ir.OpMul, A: x, B: constant(pb, entry, 3)})
			left, right, join := pb.NewBlock(), pb.NewBlock(), pb.NewBlock(u64)
			pb.Branch(entry, isZero(y), left, nil, right, nil)
			pb.Jump(left, join, pb.Append(left, ir.Instruction{Op: ir.OpAdd, A: t3, B: constant(pb, left, 1)}))
			pb.Jump(right, join, pb.Append(right, ir.Instruction{Op: ir.OpXor, A: t3, B: y}))
			p := pb.BlockParam(join, 0)
			sum := pb.Append(join, ir.Instruction{Op: ir.OpAdd, A: p, B: t3})
			pb.Return(join, pb.Append(join, ir.Instruction{Op: ir.OpAdd, A: sum, B: x}))
		}, func(x, y uint64) uint64 {
			t3 := x * 3
			p := t3 ^ y
			if y == 0 {
				p = t3 + 1
			}
			return p + t3 + x
		}},
		"loop state": {func(pb *ir.ProgramBuilder) {
			entry := pb.Entry()
			x, y := pb.Param(entry, u64), pb.Param(entry, u64)
			loop, done := pb.NewBlock(u64, u64), pb.NewBlock(u64)
			pb.Jump(entry, loop, constant(pb, entry, 0), pb.Append(entry, ir.Instruction{Op: ir.OpAnd, A: x, B: constant(pb, entry, 15)}))
			acc, i := pb.BlockParam(loop, 0), pb.BlockParam(loop, 1)
			body := pb.NewBlock()
			pb.Branch(loop, isZero(i), done, []int{acc}, body, nil)
			next := pb.Append(body, ir.Instruction{Op: ir.OpAdd, A: acc, B: pb.Append(body, ir.Instruction{Op: ir.OpMul, A: i, B: y})})
			pb.Jump(body, loop, next, pb.Append(body, ir.Instruction{Op: ir.OpSub, A: i, B: constant(pb, body, 1)}))
			pb.Return(done, pb.BlockParam(done, 0))
		}, func(x, y uint64) uint64 {
			var acc uint64
			for i := x & 15; i != 0; i-- {
				acc += i * y
			}
			return acc
		}},
		"irreducible": {func(pb *ir.ProgramBuilder) {
			entry := pb.Entry()
			x, y := pb.Param(entry, u64), pb.Param(entry, u64)
			step := pb.Append(entry, ir.Instruction{Op: ir.OpAdd, A: y, B: constant(pb, entry, 1)})
			a, c, done := pb.NewBlock(u64), pb.NewBlock(u64), pb.NewBlock(u64)
			pb.Branch(entry, compare(x, ir.BranchAbove, y), a, []int{constant(pb, entry, 0)}, c, []int{constant(pb, entry, 1)})
			pb.Jump(a, c, pb.Append(a, ir.Instruction{Op: ir.OpAdd, A: pb.BlockParam(a, 0), B: step}))
			m := pb.BlockParam(c, 0)
			pb.Branch(c, compare(m, ir.BranchAbove, constant(pb, c, 40)), done, []int{m}, a, []int{pb.Append(c, ir.Instruction{Op: ir.OpAdd, A: m, B: x})})
			pb.Return(done, pb.Append(done, ir.Instruction{Op: ir.OpAdd, A: pb.BlockParam(done, 0), B: step}))
		}, func(x, y uint64) uint64 {
			step := y + 1
			m := uint64(1)
			if x > y {
				m = step
			}
			for m <= 40 {
				m = m + x + step
			}
			return m + step
		}},
		"parallel copy permutation": {func(pb *ir.ProgramBuilder) {
			// The back edge rotates three parameters, and the exit edge
			// passes them swapped: every edge needs a parallel copy.
			entry := pb.Entry()
			x, y := pb.Param(entry, u64), pb.Param(entry, u64)
			loop, done := pb.NewBlock(u64, u64, u64, u64), pb.NewBlock(u64, u64, u64)
			pb.Jump(entry, loop, x, y, pb.Append(entry, ir.Instruction{Op: ir.OpXor, A: x, B: y}), constant(pb, entry, 5))
			a, b, c, n := pb.BlockParam(loop, 0), pb.BlockParam(loop, 1), pb.BlockParam(loop, 2), pb.BlockParam(loop, 3)
			body := pb.NewBlock()
			pb.Branch(loop, isZero(n), done, []int{c, a, b}, body, nil)
			pb.Jump(body, loop, b, c, a, pb.Append(body, ir.Instruction{Op: ir.OpSub, A: n, B: constant(pb, body, 1)}))
			p, q, r := pb.BlockParam(done, 0), pb.BlockParam(done, 1), pb.BlockParam(done, 2)
			shifted := pb.Append(done, ir.Instruction{Op: ir.OpShiftLeft, A: q, Imm: 1})
			mixed := pb.Append(done, ir.Instruction{Op: ir.OpSub, A: p, B: shifted})
			pb.Return(done, pb.Append(done, ir.Instruction{Op: ir.OpAdd, A: mixed, B: pb.Append(done, ir.Instruction{Op: ir.OpMul, A: r, B: constant(pb, done, 3)})}))
		}, func(x, y uint64) uint64 {
			a, b, c := x, y, x^y
			for range 5 {
				a, b, c = b, c, a
			}
			return c - a<<1 + b*3
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			pb := ir.NewProgramBuilder()
			tc.build(pb)
			program, err := pb.Seal()
			if err != nil {
				t.Fatal(err)
			}
			checkCrossBlockProgram(t, program, tc.want)
		})
	}
}
