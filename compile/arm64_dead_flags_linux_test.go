package compile_test

import (
	"runtime"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// arm64 selects ADD, SUB and AND without the S bit where no path reads
// NZCV, and keeps ADDS, SUBS and ANDS whose flags a branch, a select or a
// carry reuses instead of a compare.
func TestARM64DeadFlagArithmeticDropsSBit(t *testing.T) {
	type kernel struct {
		name  string
		build func(*n.Builder)
		// want and avoid are listing substrings.
		want, avoid []string
		check       func(x, y uint64) uint64
	}
	for _, k := range []kernel{
		{
			name: "dead",
			build: func(b *n.Builder) {
				x, y := b.Param(), b.Param()
				b.Return(b.And(b.Sub(b.Add(x, y), b.Const(3)), b.Const(0xff)))
			},
			want:  []string{"add X", "sub X", "and X"},
			avoid: []string{"adds ", "subs ", "ands "},
			check: func(x, y uint64) uint64 { return (x + y - 3) & 0xff },
		},
		{
			name: "zero branch reuses SUBS",
			build: func(b *n.Builder) {
				x, y := b.Param(), b.Param()
				d := b.Sub(x, y)
				zero, other := b.NewLabel(), b.NewLabel()
				b.Branch(b.IsZero(d), zero)
				b.Jump(other)
				b.Bind(zero)
				b.Return(b.Const(77))
				b.Bind(other)
				b.Return(b.Add(d, b.Const(1)))
			},
			want:  []string{"subs X"},
			check: func(x, y uint64) uint64 { return map[bool]uint64{true: 77, false: x - y + 1}[x == y] },
		},
		{
			name: "carry branch reuses ADDS",
			build: func(b *n.Builder) {
				x, y := b.Param(), b.Param()
				s := b.Add(x, y)
				carry, other := b.NewLabel(), b.NewLabel()
				b.Branch(b.Carry(s), carry)
				b.Jump(other)
				b.Bind(carry)
				b.Return(b.Xor(s, b.Const(0x55)))
				b.Bind(other)
				b.Return(s)
			},
			want: []string{"adds X"},
			check: func(x, y uint64) uint64 {
				if x+y < x {
					return (x + y) ^ 0x55
				}
				return x + y
			},
		},
		{
			name: "select reuses ANDS",
			build: func(b *n.Builder) {
				x, y := b.Param(), b.Param()
				m := b.And(x, y)
				b.Return(b.Select(m, b.Const(9), y))
			},
			want: []string{"ands X"},
			check: func(x, y uint64) uint64 {
				if x&y != 0 {
					return 9
				}
				return y
			},
		},
	} {
		t.Run(k.name, func(t *testing.T) {
			b := n.NewBuilder()
			k.build(b)
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			fn := compile.Func{ABI: abi.C}
			listing, err := lowerListing(p, compile.TargetSpec{Architecture: "arm64"}, fn)
			if err != nil {
				t.Fatal(err)
			}
			// Elision only clears S bits: without it the listing keeps every
			// flag write and is otherwise identical.
			keep, err := compile.KeepFlagWrites(compile.TargetSpec{Architecture: "arm64"})
			if err != nil {
				t.Fatal(err)
			}
			kept, err := compile.Lower(keep, p, fn)
			if err != nil {
				t.Fatal(err)
			}
			keptListing, err := kept.Listing()
			if err != nil {
				t.Fatal(err)
			}
			if got, want := strings.Split(listing, "\n"), strings.Split(keptListing, "\n"); len(got) != len(want) {
				t.Fatalf("elision changed the instruction count:\n%s\nwithout elision:\n%s", listing, keptListing)
			} else {
				for i := range got {
					if got[i] == want[i] {
						continue
					}
					flagFree := strings.NewReplacer("adds ", "add ", "subs ", "sub ", "ands ", "and ").Replace(want[i])
					if got[i] != flagFree {
						t.Fatalf("line %d %q, without elision %q", i, got[i], want[i])
					}
				}
			}
			for _, s := range k.want {
				if !strings.Contains(listing, s) {
					t.Errorf("listing lacks %q:\n%s", s, listing)
				}
			}
			for _, s := range k.avoid {
				if strings.Contains(listing, s) {
					t.Errorf("listing has %q:\n%s", s, listing)
				}
			}
			if runtime.GOARCH != "arm64" {
				return
			}
			code, err := n.Compile(p, compile.TargetSpec{}, fn)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			values := []uint64{0, 1, 2, 3, 0xff, 1 << 63, ^uint64(0), 0x123456789abcdef0}
			for _, x := range values {
				for _, y := range values {
					got, err := code.Call(uintptr(x), uintptr(y))
					if err != nil {
						t.Fatal(err)
					}
					if want := k.check(x, y); uint64(got) != want {
						t.Fatalf("f(%#x, %#x) = %#x, want %#x\n%s", x, y, got, want, listing)
					}
				}
			}
		})
	}
}
