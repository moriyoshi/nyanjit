package compile_test

import (
	"fmt"
	"math/bits"
	"regexp"
	"runtime"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// i386NarrowOps are 8- and 16-bit operations whose 386 form ends with its own
// mask, which a separate narrowing of the result used to repeat, with their
// Go reference at width w and the zero extensions the optimized tier emits
// returning them: one per parameter used and one per masked operation.
var i386NarrowOps = []struct {
	name       string
	build      func(b *n.Builder, x, y n.Value) n.Value
	eval       func(w uint8, x, y uint64) uint64
	extensions int
}{
	{"add", (*n.Builder).Add, func(_ uint8, x, y uint64) uint64 { return x + y }, 3},
	{"sub", (*n.Builder).Sub, func(_ uint8, x, y uint64) uint64 { return x - y }, 3},
	{"mul", (*n.Builder).Mul, func(_ uint8, x, y uint64) uint64 { return x * y }, 3},
	{"xor", (*n.Builder).Xor, func(_ uint8, x, y uint64) uint64 { return x ^ y }, 3},
	{"shl0", func(b *n.Builder, x, _ n.Value) n.Value { return b.ShiftLeft(x, 0) }, func(_ uint8, x, _ uint64) uint64 { return x }, 2},
	{"shl", func(b *n.Builder, x, _ n.Value) n.Value { return b.ShiftLeft(x, 5) }, func(_ uint8, x, _ uint64) uint64 { return x << 5 }, 2},
	{"rotl", func(b *n.Builder, x, _ n.Value) n.Value { return b.RotateLeft(x, 3) }, func(w uint8, x, _ uint64) uint64 {
		if w == 8 {
			return uint64(bits.RotateLeft8(uint8(x), 3))
		}
		return uint64(bits.RotateLeft16(uint16(x), 3))
	}, 4},
	{"sub-add", func(b *n.Builder, x, y n.Value) n.Value { return b.Add(b.Sub(x, y), b.Mul(x, y)) },
		func(_ uint8, x, y uint64) uint64 { return x - y + x*y }, 5},
}

// i386NarrowCallee returns its 32-bit argument, so a caller sees every bit
// of the register it passes.
func i386NarrowCallee(b *n.Builder) { b.Return(b.ParamBits(32)) }

// i386NarrowKernel returns op of two w-bit parameters. Across blocks it
// passes the result widened to 32 bits to target and returns what the call
// returns when x is not zero, and otherwise the result, kept across the call,
// widened and plus one, so either way a bit left above w shows.
func i386NarrowKernel(w uint8, build func(b *n.Builder, x, y n.Value) n.Value, target uintptr, across bool) func(*n.Builder) {
	return func(b *n.Builder) {
		x, y := b.ParamBits(w), b.ParamBits(w)
		r := build(b, x, y)
		if !across {
			b.Return(r)
			return
		}
		called := b.CallC(b.ConstNative(target), b.CastUnsigned(r, 32))
		zero := b.NewLabel()
		b.Branch(b.IsZero(x), zero)
		b.Return(b.CastUnsigned(called, 32))
		b.Bind(zero)
		b.Return(b.Add(b.CastUnsigned(r, 32), b.ConstBits(32, 1)))
	}
}

// TestI386NarrowAfterMask: a narrowing right after an 8- or 16-bit operation
// that already masked its result emits nothing more, so no register is
// zero-extended twice in a row, and the returned value of op(x, y) has just
// the operation's mask after the parameters' two. On 386 the results match
// Go, returned directly, across a block and passed to a C call.
func TestI386NarrowAfterMask(t *testing.T) {
	extension := regexp.MustCompile(`^(?:movzx (E\w+), \w+|and (E\w+), 0xff(?:ff)?)$`)
	// repeated returns a zero extension of the register the line before
	// zero-extended.
	repeated := func(listing string) string {
		last := ""
		for _, line := range strings.Split(listing, "\n") {
			m := extension.FindStringSubmatch(line)
			if m == nil {
				last = ""
				continue
			}
			r := m[1] + m[2]
			if r == last {
				return line
			}
			last = r
		}
		return ""
	}
	values := []uint64{0, 1, 2, 7, 0x5a, 0x7f, 0x80, 0xfe, 0xff, 0x100, 0x7fff, 0x8000, 0xfffe, 0xffff, 0x1234}
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}}
	var target uintptr
	if runtime.GOARCH == "386" {
		callee, err := lower386(t, compile.TierOptimized, i386NarrowCallee, f).Template()
		if err != nil {
			t.Fatal(err)
		}
		code, err := callee.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer code.Close()
		if target, err = code.EntryAddress(); err != nil {
			t.Fatal(err)
		}
	}
	for _, w := range []uint8{8, 16} {
		mask := uint64(1)<<w - 1
		for _, o := range i386NarrowOps {
			for _, across := range []bool{false, true} {
				for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
					name := fmt.Sprintf("%s-%d/across=%v/tier=%v", o.name, w, across, tier)
					kf := f
					if across {
						if runtime.GOARCH != "386" {
							continue // a host builder's native call result is wider
						}
						kf.Context = compile.ContextForeign
					}
					u := lower386(t, tier, i386NarrowKernel(w, o.build, target, across), kf)
					listing, err := u.Listing()
					if err != nil {
						t.Fatal(err)
					}
					if m := repeated(listing); m != "" {
						t.Errorf("%s: zero-extended twice: %q\n%s", name, m, listing)
					}
					if want := o.extensions; !across && tier == compile.TierOptimized {
						got := 0
						for _, line := range strings.Split(listing, "\n") {
							if extension.MatchString(line) {
								got++
							}
						}
						if got != want {
							t.Errorf("%s: %d zero extensions, want %d\n%s", name, got, want, listing)
						}
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
							got, high, err := code.CallPair(uintptr(x), uintptr(y))
							if err != nil {
								t.Fatal(err)
							}
							if high != 0 {
								t.Fatalf("%s: f(%#x, %#x) high word %#x", name, x, y, high)
							}
							want := o.eval(w, x, y) & mask
							if across && x == 0 {
								want++
							}
							if uint64(got) != want {
								t.Fatalf("%s: f(%#x, %#x) = %#x, want %#x\n%s", name, x, y, got, want, listing)
							}
						}
					}
					code.Close()
				}
			}
		}
	}
}
