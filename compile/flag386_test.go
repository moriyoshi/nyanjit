package compile_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// flag386Builds branch on 386 arithmetic right after it. Each returns the
// result on both sides, distinguished, or a constant on each when it
// returns no value.
var flag386Builds = []struct {
	name string
	w    uint8
	// build returns the predicate on the arithmetic of x and y.
	build func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value)
	// want matches the listing of the optimized tier; never must not.
	want, never string
	// platform limits the case to a platform frame: without one, a 64-bit
	// overflow check needs a workspace and keeps the computed form.
	platform bool
}{
	{name: "add-zero", w: 32, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		s := b.Add(x, y)
		return b.IsZero(s), s
	}, want: `\nadd E\w+, E\w+\nj(e|ne) `, never: `cmp`},
	{name: "sub-borrow", w: 32, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		return b.Compare(x, ir.BranchBelow, y), b.Sub(x, y)
	}, want: `\nsub E\w+, E\w+\nj(b|ae) `, never: `cmp`},
	{name: "sub-sign", w: 32, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		s := b.Sub(x, y)
		return b.CompareImmediate(s, ir.BranchLess, 0), s
	}, want: `\nsub E\w+, E\w+\n(mov [^\n]*\n)*sar E\w+, 31\n(mov [^\n]*\n)*j(s|ns) `, never: `cmp`},
	{name: "inc-zero", w: 32, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		s := b.Add(x, b.ConstBits(32, 1))
		return b.IsZero(s), s
	}, want: `\ninc E\w+\nj(e|ne) `, never: `cmp`},
	// INC keeps the carry of the ADD before it.
	{name: "add-inc-carry", w: 32, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		s := b.Add(x, y)
		return b.Compare(s, ir.BranchBelow, x), b.Add(s, b.ConstBits(32, 1))
	}, want: `\nadd E\w+, E\w+\n(mov [^\n]*\n)*inc E\w+\nj(b|ae) `, never: `cmp`},
	{name: "overflow-add", w: 32, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		return b.Overflow(b.Add(x, y)), nyanjit.Value{}
	}, want: `\nadd E\w+, E\w+\nj(o|no) `, never: `cmp|sar`},
	{name: "overflow-sub", w: 32, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		return b.Overflow(b.Sub(x, y)), nyanjit.Value{}
	}, want: `\nsub E\w+, E\w+\nj(o|no) `, never: `cmp|sar`},
	// The 32-bit IMUL's OF is the signed overflow of its product.
	{name: "overflow-mul", w: 32, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		return b.Overflow(b.Mul(x, y)), nyanjit.Value{}
	}, want: `\nimul E\w+, E\w+\nj(o|no) `, never: `cmp|imul E\w+\n|shr`},
	// A pair's ADC or SBB leaves the 64-bit sign, carry and overflow.
	{name: "overflow-add-64", w: 64, platform: true, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		return b.Overflow(b.Add(x, y)), nyanjit.Value{}
	}, want: `\nadd E\w+, E\w+\n(mov [^\n]*\n)*adc E\w+, E\w+\nj(o|no) `, never: `cmp|xor`},
	{name: "overflow-sub-64", w: 64, platform: true, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		return b.Overflow(b.Sub(x, y)), nyanjit.Value{}
	}, want: `\nsub E\w+, E\w+\n(mov [^\n]*\n)*sbb E\w+, E\w+\nj(o|no) `, never: `cmp|xor`},
	{name: "add-carry-64", w: 64, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		s := b.Add(x, y)
		return b.Compare(s, ir.BranchBelow, x), s
	}, want: `\nadc E\w+, E\w+\n(mov [^\n]*\n)*j(b|ae) `, never: `cmp`},
	{name: "sub-borrow-64", w: 64, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		return b.Compare(x, ir.BranchBelow, y), b.Sub(x, y)
	}, want: `\nsbb E\w+, E\w+\n(mov [^\n]*\n)*j(b|ae) `, never: `cmp`},
	{name: "add-sign-64", w: 64, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		s := b.Add(x, y)
		return b.CompareImmediate(s, ir.BranchLess, 0), s
	}, want: `\nadc E\w+, E\w+\n(mov [^\n]*\n)*j(s|ns) `, never: `cmp`},
	// The high word's zero is not the pair's: these compare both words.
	{name: "add-zero-64", w: 64, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		s := b.Add(x, y)
		return b.IsZero(s), s
	}, want: `\nadc E\w+, E\w+\n(.*\n)*cmp `, never: `\nadc E\w+, E\w+\n(mov [^\n]*\n)*j`},
	{name: "and-sign-64", w: 64, build: func(b *nyanjit.Builder, x, y nyanjit.Value) (nyanjit.Predicate, nyanjit.Value) {
		s := b.And(x, y)
		return b.CompareImmediate(s, ir.BranchLess, 0), s
	}, want: `\nand E\w+, E\w+\n(.*\n)*cmp `, never: `\nand E\w+, E\w+\n(mov [^\n]*\n)*j`},
}

// TestI386FlagListings lowers flag386Builds for 386 at the optimized tier:
// the branch reads the flags of the arithmetic.
func TestI386FlagListings(t *testing.T) {
	funcs := []compile.Func{
		{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}},
		{ABI: abi.C},
	}
	// The online allocator keeps the computed overflow form, on amd64 too.
	online := os.Getenv("NYANJIT_INTERNAL_ONLINE_ALLOCATION") == "1"
	for _, c := range flag386Builds {
		if online && strings.HasPrefix(c.name, "overflow-") {
			continue
		}
		for fi, f := range funcs {
			if c.platform && f.Stack.Practice != abi.StackPlatform {
				continue
			}
			u := lower386(t, compile.TierOptimized, func(b *nyanjit.Builder) {
				x, y := b.ParamBits(c.w), b.ParamBits(c.w)
				p, r := c.build(b, x, y)
				yes := b.NewLabel()
				b.Branch(p, yes)
				if r == (nyanjit.Value{}) {
					// The arithmetic is used only by the check.
					b.Return(b.ConstBits(c.w, 1))
					b.Bind(yes)
					b.Return(b.ConstBits(c.w, 2))
					return
				}
				b.Return(b.Xor(r, b.ConstBits(c.w, 0x5a)))
				b.Bind(yes)
				b.Return(r)
			}, f)
			listing, err := u.Listing()
			if err != nil {
				t.Fatal(err)
			}
			if !regexp.MustCompile(c.want).MatchString(listing) || regexp.MustCompile(c.never).MatchString(listing) {
				t.Errorf("%s/f%d: want %q, never %q:\n%s", c.name, fi, c.want, c.never, listing)
			}
		}
	}
}
