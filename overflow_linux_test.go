//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386 || loong64)

package nyanjit_test

import (
	"math/big"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

// TestOverflowNative runs Builder.Overflow from NewBuilderFor, both as a
// branch and as a value, on the host at both tiers against exact signed
// arithmetic.
func TestOverflowNative(t *testing.T) {
	operations := []struct {
		build func(b *nyanjit.Builder, x, y nyanjit.Value) nyanjit.Value
		exact func(x, y *big.Int) *big.Int
	}{
		{(*nyanjit.Builder).Add, func(x, y *big.Int) *big.Int { return new(big.Int).Add(x, y) }},
		{(*nyanjit.Builder).Sub, func(x, y *big.Int) *big.Int { return new(big.Int).Sub(x, y) }},
		{(*nyanjit.Builder).Mul, func(x, y *big.Int) *big.Int { return new(big.Int).Mul(x, y) }},
	}
	for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
		target, err := compile.NewTarget(compile.TargetSpec{Tier: tier})
		if err != nil {
			t.Fatal(err)
		}
		for n, o := range operations {
			for _, w := range []uint8{8, 16, 32} {
				for _, value := range []bool{false, true} {
					b := nyanjit.NewBuilderFor(target, compile.Func{ABI: abi.C})
					x, y := b.ParamBits(w), b.ParamBits(w)
					r := o.build(b, x, y)
					if value {
						b.Return(b.CastUnsigned(b.PredicateValue(b.Overflow(r), w), 32))
					} else {
						trap := b.NewLabel()
						b.Branch(b.Overflow(r), trap)
						b.Return(b.ConstBits(32, 0))
						b.Bind(trap)
						b.Return(b.ConstBits(32, 1))
					}
					unit, err := b.Lower()
					if err != nil {
						t.Fatal(err)
					}
					template, err := unit.Template()
					if err != nil {
						t.Fatal(err)
					}
					code, err := template.Bind(nil)
					if err != nil {
						t.Fatal(err)
					}
					mask := uint64(1)<<w - 1
					limit := new(big.Int).Lsh(big.NewInt(1), uint(w-1))
					for _, a := range []uint64{0, 1, 2, mask >> 1, mask>>1 + 1, mask, mask - 1, 0x5a5a5a5a & mask} {
						for _, c := range []uint64{0, 1, mask, mask >> 1, mask>>1 + 1, 3} {
							exact := o.exact(big.NewInt(int64(a<<(64-w))>>(64-w)), big.NewInt(int64(c<<(64-w))>>(64-w)))
							want := uint64(0)
							if exact.Cmp(new(big.Int).Neg(limit)) < 0 || exact.Cmp(limit) >= 0 {
								want = 1
							}
							words := []any{uint32(a), uint32(c)}
							if w == 8 {
								words = []any{uint8(a), uint8(c)}
							} else if w == 16 {
								words = []any{uint16(a), uint16(c)}
							}
							got, err := code.Call(execution.Words(words...)...)
							if err != nil || uint64(got)&0xffffffff != want {
								t.Errorf("tier %d operation %d width %d value %v: f(%#x, %#x) = %#x, %v; want %d", tier, n, w, value, a, c, got, err, want)
							}
						}
					}
					code.Close()
				}
			}
		}
	}
}
