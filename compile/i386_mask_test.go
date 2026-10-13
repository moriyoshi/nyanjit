package compile_test

import (
	"fmt"
	"regexp"
	"runtime"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// i386MaskOps are 8- and 16-bit operations whose result the 386 target
// zero-extends, with their Go reference.
var i386MaskOps = []struct {
	name  string
	build func(b *n.Builder, x, y n.Value) n.Value
	eval  func(x, y uint64) uint64
}{
	{"add", (*n.Builder).Add, func(x, y uint64) uint64 { return x + y }},
	{"sub", (*n.Builder).Sub, func(x, y uint64) uint64 { return x - y }},
	{"and", (*n.Builder).And, func(x, y uint64) uint64 { return x & y }},
	{"or", (*n.Builder).Or, func(x, y uint64) uint64 { return x | y }},
	{"xor", (*n.Builder).Xor, func(x, y uint64) uint64 { return x ^ y }},
	{"mul", (*n.Builder).Mul, func(x, y uint64) uint64 { return x * y }},
	{"shl", func(b *n.Builder, x, _ n.Value) n.Value { return b.ShiftLeft(x, 3) }, func(x, _ uint64) uint64 { return x << 3 }},
	{"shr", func(b *n.Builder, x, _ n.Value) n.Value { return b.ShiftRight(x, 3) }, func(x, _ uint64) uint64 { return x >> 3 }},
	{"narrow", func(b *n.Builder, x, y n.Value) n.Value {
		return b.CastUnsigned(b.Add(b.CastUnsigned(x, 32), b.CastUnsigned(y, 32)), uint8(x.Bits()))
	},
		func(x, y uint64) uint64 { return x + y }},
}

// i386MaskKernel returns op of two w-bit parameters. When read, a branch
// tests the result for zero; otherwise the result is combined with x, which
// stays live, so the operation writes another register.
func i386MaskKernel(w uint8, build func(b *n.Builder, x, y n.Value) n.Value, read bool) func(*n.Builder) {
	return func(b *n.Builder) {
		x, y := b.ParamBits(w), b.ParamBits(w)
		r := build(b, x, y)
		if !read {
			b.Return(b.Xor(r, x))
			return
		}
		zero := b.NewLabel()
		b.Branch(b.IsZero(r), zero)
		b.Return(b.Xor(r, b.ConstBits(w, 0x5a)))
		b.Bind(zero)
		b.Return(b.ConstBits(w, 0x99))
	}
}

// TestI386MaskListings: in the optimized tier, a narrow result whose flags
// no path reads is zero-extended by MOVZX, and one whose zero decides a
// branch keeps the AND that sets it, with no compare; the baseline masks
// with MOVZX in both cases. On 386 every kernel computes Go's result.
func TestI386MaskListings(t *testing.T) {
	values := []uint64{0, 1, 2, 0x5a, 0x7f, 0x80, 0xff, 0x100, 0x7fff, 0x8000, 0xffff, 0x1234}
	movzx := regexp.MustCompile(`\nmovzx E\w+, \w+\n`)
	read := regexp.MustCompile(`\nand (E\w+), 0x(ff|ffff)\nj(e|nz?) `)
	for _, w := range []uint8{8, 16} {
		mask := uint64(1)<<w - 1
		for _, o := range i386MaskOps {
			for _, flags := range []bool{false, true} {
				for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
					name := fmt.Sprintf("%s-%d/read=%v/tier=%v", o.name, w, flags, tier)
					u := lower386(t, tier, i386MaskKernel(w, o.build, flags), compile.Func{ABI: abi.C})
					listing, err := u.Listing()
					if err != nil {
						t.Fatal(err)
					}
					if !movzx.MatchString(listing) {
						t.Errorf("%s: no MOVZX:\n%s", name, listing)
					}
					if want := flags && tier == compile.TierOptimized; read.MatchString(listing) != want {
						t.Errorf("%s: AND before the branch %v, want %v:\n%s", name, !want, want, listing)
					}
					if runtime.GOARCH != "386" {
						continue
					}
					template, err := u.Template()
					if err != nil {
						t.Fatal(err)
					}
					code, err := template.Bind(nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, x := range values {
						for _, y := range values {
							x, y := x&mask, y&mask
							lo, _, err := code.CallPair(uintptr(x), uintptr(y))
							if err != nil {
								t.Fatal(err)
							}
							r := o.eval(x, y) & mask
							want := r ^ x
							if flags && r == 0 {
								want = 0x99
							} else if flags {
								want = r ^ 0x5a
							}
							if uint64(lo) != want {
								t.Fatalf("%s: f(%#x, %#x) = %#x, want %#x\n%s", name, x, y, lo, want, listing)
							}
						}
					}
					code.Close()
				}
			}
		}
	}
}
