package nyanjit_test

import (
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/irinterp"
)

// TestOverflowSemantics checks Builder.Overflow and its value form against
// exact signed arithmetic, through the reference interpreter.
func TestOverflowSemantics(t *testing.T) {
	signed := func(x uint64, w uint8) *big.Int {
		return big.NewInt(int64(x<<(64-w)) >> (64 - w))
	}
	operations := []struct {
		name  string
		build func(b *nyanjit.Builder, x, y nyanjit.Value) nyanjit.Value
		exact func(x, y *big.Int) *big.Int
	}{
		{"add", (*nyanjit.Builder).Add, func(x, y *big.Int) *big.Int { return new(big.Int).Add(x, y) }},
		{"sub", (*nyanjit.Builder).Sub, func(x, y *big.Int) *big.Int { return new(big.Int).Sub(x, y) }},
		{"mul", (*nyanjit.Builder).Mul, func(x, y *big.Int) *big.Int { return new(big.Int).Mul(x, y) }},
	}
	for _, o := range operations {
		for _, w := range []uint8{8, 16, 32, 64} {
			b := nyanjit.NewBuilder()
			x, y := b.ParamBits(w), b.ParamBits(w)
			r := o.build(b, x, y)
			b.Return(b.Add(b.ShiftLeft(b.CastUnsigned(b.PredicateValue(b.Overflow(r), w), 64), 1), b.CastUnsigned(b.PredicateValue(b.Overflow(r).Not(), 8), 64)))
			program, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			mask := uint64(1)<<(w-1)<<1 - 1
			values := []uint64{0, 1, 2, 3, mask >> 1, mask>>1 + 1, mask, mask - 1, mask >> 2, 0x5a5a5a5a5a5a5a5a & mask}
			min, max := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), uint(w-1))), new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(w-1)), big.NewInt(1))
			for _, a := range values {
				for _, c := range values {
					result, err := irinterp.Run(program, []uint64{a, c}, nil)
					if err != nil {
						t.Fatal(err)
					}
					exact := o.exact(signed(a, w), signed(c, w))
					want := uint64(1)
					if exact.Cmp(min) < 0 || exact.Cmp(max) > 0 {
						want = 2
					}
					if !result.Matches(want, 0) {
						t.Errorf("%s %d: %#x, %#x: got %v, want %d", o.name, w, a, c, result, want)
					}
				}
			}
		}
	}
}

// TestOverflowBranchReadsFlag checks that an overflow branch on a sum,
// difference or 64-bit product the block computes for the check alone tests
// the overflow flag: JO on amd64 and B.VS on arm64.
func TestOverflowBranchReadsFlag(t *testing.T) {
	if os.Getenv("NYANJIT_INTERNAL_ONLINE_ALLOCATION") == "1" {
		t.Skip("the online allocator keeps the computed overflow test")
	}
	for _, c := range []struct {
		arch, want string
		mul        bool
	}{{"amd64", "jo ", true}, {"arm64", "b.vs ", false}} {
		target, err := compile.NewTarget(compile.TargetSpec{Architecture: c.arch})
		if err != nil {
			t.Fatal(err)
		}
		operations := []func(*nyanjit.Builder, nyanjit.Value, nyanjit.Value) nyanjit.Value{(*nyanjit.Builder).Add, (*nyanjit.Builder).Sub}
		if c.mul {
			operations = append(operations, (*nyanjit.Builder).Mul)
		}
		for n, operation := range operations {
			for _, w := range []uint8{32, 64} {
				if n == 2 && w == 32 {
					continue
				}
				b := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.C})
				x, y := b.ParamBits(w), b.ParamBits(w)
				trap := b.NewLabel()
				b.Branch(b.Overflow(operation(b, x, y)), trap)
				b.Return(b.Const(1))
				b.Bind(trap)
				b.Return(b.Const(0))
				unit, err := b.Lower()
				if err != nil {
					t.Fatal(err)
				}
				listing, err := unit.Listing()
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(listing, c.want) {
					t.Errorf("%s operation %d, %d bits: no %q:\n%s", c.arch, n, w, c.want, listing)
				}
			}
		}
	}
}
