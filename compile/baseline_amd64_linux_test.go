//go:build linux && amd64

package compile_test

import (
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestBaselineAMD64FixedRegisters runs operations with fixed registers
// (RDX:RAX, CL), two-address aliasing, narrow widths and floats against the
// optimized tier.
func TestBaselineAMD64FixedRegisters(t *testing.T) {
	platform := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}}
	all := [][]uint64{{7, 3, 65, 1 << 40, 9, 2}, {^uint64(0), 1, 63, 5, 0, 11}, {1 << 63, ^uint64(0), 1, 2, 3, 4}}
	args := func(n int) [][]uint64 {
		out := make([][]uint64, len(all))
		for i, a := range all {
			out[i] = a[:n]
		}
		return out
	}
	runBoth(t, "divide-shift", func(b *nyanjit.Builder) {
		x, y, z, w := b.Param(), b.Param(), b.Param(), b.Param()
		q := b.Div(x, y)
		r := b.RemSigned(w, b.Or(y, b.Const(1)))
		s := b.ShiftLeftBy(z, q)
		u := b.RotateRightBy(w, z)
		v := b.ShiftRightArithmeticBy(x, b.And(r, b.Const(63)))
		h := b.MulHigh(x, w)
		hs := b.MulHighSigned(z, x)
		b.Return(b.Xor(b.Xor(b.Add(q, r), b.Sub(s, u)), b.Xor(v, b.Sub(h, hs))))
	}, platform, args(4))
	runBoth(t, "narrow", func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		x8, y8 := b.CastUnsigned(x, 8), b.CastUnsigned(b.Or(y, b.Const(1)), 8)
		x16, y16 := b.CastUnsigned(x, 16), b.CastUnsigned(y, 16)
		a := b.Sub(b.Mul(x8, y8), b.DivSigned(x8, y8))
		c := b.Sub(b.Add(x16, b.ConstBits(16, 0xfff0)), b.ShiftLeftBy(y16, b.CastUnsigned(x, 16)))
		d := b.Sub(b.CastUnsigned(y, 32), b.CastUnsigned(x, 32))
		b.Return(b.Xor(b.Xor(b.CastUnsigned(a, 64), b.CastUnsigned(c, 64)), b.CastUnsigned(d, 64)))
	}, platform, args(2))
	runBoth(t, "float", func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		fx := b.FFromSigned(x, 64)
		fy := b.FFromUnsigned(b.Or(y, b.Const(1)), 64)
		q := b.FDiv(fy, fx)
		s := b.FSqrt(b.FConvert(b.FSub(fy, q), 32))
		e := b.FPow(fy, b.Float64(1.5)) // uses the exact product error
		big := b.FComparePredicate(e, ir.FloatGreater, b.Float64(math.Inf(1)))
		r := b.Select(b.CastUnsigned(b.FCompare(s, ir.FloatOrdered, s), 64), b.CastUnsigned(s, 64), b.Const(7))
		done := b.NewLabel()
		b.Branch(big, done)
		b.Return(b.Xor(r, b.FToSigned(b.FAdd(e, q), 64)))
		b.Bind(done)
		b.Return(b.Const(1))
	}, platform, args(2))
}
