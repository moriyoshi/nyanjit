//go:build linux && 386

package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// baseline386Extra are kernels for the 386 pair expansions.
var baseline386Extra = []struct {
	name  string
	build func(*nyanjit.Builder)
	args  [][]uint64
}{
	{"wide-ops", func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		r := b.Xor(b.MulHigh(x, y), b.MulHighSigned(x, y))
		r = b.Add(r, b.Div(x, b.Or(y, b.Const(1))))
		r = b.Xor(r, b.RemSigned(x, b.Or(y, b.Const(3))))
		r = b.Add(r, b.ShiftLeftBy(x, y))
		r = b.Xor(r, b.ShiftRightArithmeticBy(y, x))
		r = b.Add(r, b.RotateLeftBy(x, b.Add(y, b.Const(7))))
		r = b.Xor(r, b.Select(b.And(x, b.Const(1<<40)), x, y))
		r = b.Add(r, b.LeadingZeros(x))
		r = b.Add(r, b.TrailingZeros(y))
		r = b.Add(r, b.SignExtend(b.CastUnsigned(x, 16), 16))
		b.Return(r)
	}, [][]uint64{{0, 0}, {1, 2}, {^uint64(0), 63}, {1 << 63, 1}, {0x123456789abcdef0, 0xfedcba9876543210}, {7, 1 << 40}}},
	{"compare64", func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		count := b.Var(b.Const(0))
		for k, c := range []ir.BranchCondition{ir.BranchEqual, ir.BranchNotEqual, ir.BranchLess, ir.BranchGreaterEqual, ir.BranchLessEqual, ir.BranchGreater, ir.BranchBelow, ir.BranchAboveEqual, ir.BranchBelowEqual, ir.BranchAbove} {
			skip := b.NewLabel()
			b.Branch(b.Compare(x, c^1, y), skip)
			b.Set(count, b.Or(b.Get(count), b.Const(1<<k)))
			b.Bind(skip)
		}
		b.Return(b.Get(count))
	}, [][]uint64{{0, 0}, {1, 2}, {2, 1}, {^uint64(0), 1}, {1, ^uint64(0)}, {1 << 32, 1}, {1, 1 << 32}, {1 << 63, 1<<63 - 1}}},
	{"narrow", func(b *nyanjit.Builder) {
		x := b.CastUnsigned(b.Param(), 16)
		y := b.CastUnsigned(b.Param(), 16)
		r := b.Add(b.Mul(x, y), b.RotateLeft(x, 3))
		r = b.Xor(r, b.DivSigned(x, b.Or(y, b.ConstBits(16, 1))))
		r = b.Add(r, b.ShiftRightBy(x, b.And(y, b.ConstBits(16, 31))))
		r = b.Add(r, b.LeadingZeros(y))
		b.Return(b.CastUnsigned(r, 64))
	}, [][]uint64{{0, 0}, {0xffff, 0xffff}, {0x8000, 3}, {1234, 5678}}},
	{"float", func(b *nyanjit.Builder) {
		x := b.FFromSigned(b.Param(), 64)
		y := b.FFromUnsigned(b.Param(), 64)
		z := b.FDiv(b.FAdd(x, y), b.FSqrt(b.FAdd(b.FMul(y, y), b.Float64(1))))
		r := b.FToSigned(b.FMul(z, b.Float64(1e6)), 64)
		r = b.Add(r, b.CastUnsigned(b.FToUnsigned(b.FConvert(b.FConvert(z, 32), 64), 32), 64))
		b.Return(r)
	}, [][]uint64{{0, 0}, {1, 2}, {^uint64(0), 1 << 63}, {1 << 40, 3}}},
}

func run386Both(t *testing.T, name string, build func(*nyanjit.Builder), args [][]uint64) {
	t.Helper()
	b := nyanjit.NewBuilder()
	build(b)
	p, err := b.Finish()
	if err != nil {
		t.Fatalf("%s: finish: %v", name, err)
	}
	for _, f := range []compile.Func{
		{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}},
		{ABI: abi.C},
	} {
		opt, err := nyanjit.Compile(p, compile.TargetSpec{}, f)
		if err != nil {
			if f.Stack.Practice == abi.StackPlatform {
				continue // a frame over Code.Call's limit
			}
			t.Fatalf("%s: optimized: %v", name, err)
		}
		base, err := nyanjit.Compile(p, compile.TargetSpec{Tier: compile.TierBaseline}, f)
		if err != nil {
			opt.Close()
			if f.Stack.Practice == abi.StackPlatform {
				continue // a frame over Code.Call's limit
			}
			t.Fatalf("%s: baseline: %v", name, err)
		}
		for _, in := range args {
			words := make([]any, len(in))
			for i, x := range in {
				words[i] = x
			}
			lo, hi, err := opt.CallPair(execution.Words(words...)...)
			if err != nil {
				t.Fatal(err)
			}
			blo, bhi, err := base.CallPair(execution.Words(words...)...)
			if err != nil {
				t.Fatal(err)
			}
			if lo != blo || hi != bhi {
				target, _ := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
				unit, _ := compile.Lower(target, p, f)
				listing, _ := unit.Listing()
				t.Errorf("%s%v stack=%v: baseline %#x:%#x, optimized %#x:%#x\n%s", name, in, f.Stack.Practice, bhi, blo, hi, lo, listing)
			}
		}
		opt.Close()
		base.Close()
	}
}

func TestBaseline386Kernels(t *testing.T) {
	for _, k := range baseline386Kernels {
		run386Both(t, k.name, k.build, k.args)
	}
	for _, k := range baseline386Extra {
		run386Both(t, k.name, k.build, k.args)
	}
}
