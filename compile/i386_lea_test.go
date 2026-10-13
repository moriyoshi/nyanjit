package compile_test

import (
	"regexp"
	"runtime"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

// i386LEABuilds are 386 kernels whose additions, subtractions of an
// immediate, increments and decrements write a register other than their
// first operand. Each takes two parameters of w bits.
var i386LEABuilds = []struct {
	name  string
	w     uint8
	build func(b *n.Builder, x, y n.Value)
	// want matches the listing of the optimized tier for compile.Func{ABI:
	// abi.C}; never must not.
	want, never string
}{
	{name: "straight", w: 32, build: func(b *n.Builder, x, y n.Value) {
		b.Return(b.Add(b.Add(x, b.ConstBits(32, 7)), b.Mul(x, y)))
	}, want: `\nlea ECX, \[EAX \+ 7\]\n`, never: `\nadd ECX, 0x7`},
	{name: "inc-dec", w: 32, build: func(b *n.Builder, x, y n.Value) {
		s := b.Add(x, b.ConstBits(32, 1))
		u := b.Sub(y, b.ConstBits(32, 1))
		v := b.Sub(x, b.ConstBits(32, 1000))
		b.Return(b.Xor(b.Xor(s, x), b.Xor(b.Xor(u, y), v)))
	}, want: `\nlea E\w+, \[E\w+ \+ 1\]\n(.*\n)*lea E\w+, \[E\w+ \+ -1\]\n`, never: `\n(inc|dec) `},
	{name: "sum", w: 32, build: func(b *n.Builder, x, y n.Value) {
		b.Return(b.Xor(b.Xor(b.Add(x, y), x), y))
	}, want: `\nlea E\w+, \[E\w+ \+ E\w+\*1 \+ 0\]\n`, never: `\nadd `},
	// The branch reads the addition's zero flag, so ADD stays, though x
	// lives on and the sum goes to another register.
	{name: "zero read", w: 32, build: func(b *n.Builder, x, y n.Value) {
		s := b.Add(x, y)
		zero := b.NewLabel()
		b.Branch(b.IsZero(s), zero)
		b.Return(b.Xor(b.Add(s, b.ConstBits(32, 5)), x))
		b.Bind(zero)
		b.Return(b.ConstBits(32, 99))
	}, want: `\nmov EDX, EAX\nadd EDX, ECX\nje `, never: `\nlea [^\n]*\nje `},
	// A narrow register addition is LEA, then MOVZX for its mask.
	{name: "narrow", w: 8, build: func(b *n.Builder, x, y n.Value) {
		s := b.Add(x, y)
		t := b.Add(x, b.ConstBits(8, 200))
		b.Return(b.Xor(b.Xor(s, x), b.Xor(t, y)))
	}, want: `\nlea EDX, \[EAX \+ ECX\*1 \+ 0\]\nmovzx EDX, DL\n`, never: `\nand E\w+, 0xff\n`},
	{name: "pair", w: 64, build: func(b *n.Builder, x, y n.Value) {
		b.Return(b.Xor(b.Xor(b.Add(x, y), x), y))
	}, want: `\nadd E\w+, E\w+\n(mov [^\n]*\n)*adc E\w+, E\w+\n`, never: `\nlea `},
}

// TestI386AdditionWithDeadFlagsUsesLEA checks the listings of
// i386LEABuilds.
func TestI386AdditionWithDeadFlagsUsesLEA(t *testing.T) {
	for _, c := range i386LEABuilds {
		u := lower386(t, compile.TierOptimized, func(b *n.Builder) { c.build(b, b.ParamBits(c.w), b.ParamBits(c.w)) }, compile.Func{ABI: abi.C})
		listing, err := u.Listing()
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(c.want).MatchString(listing) || regexp.MustCompile(c.never).MatchString(listing) {
			t.Errorf("%s: want %q, never %q:\n%s", c.name, c.want, c.never, listing)
		}
	}
}

// i386LEARows returns the instructions of a 386 listing, branch targets
// dropped: LEA moves them.
func i386LEARows(listing string) []string {
	var rows []string
	for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
		if strings.HasPrefix(line, "j") {
			line, _, _ = strings.Cut(line, " ")
		}
		rows = append(rows, line)
	}
	return rows
}

// TestI386DeadFlagElisionOnlySelectsLEA lowers i386LEABuilds and the flag
// reuse kernels of flag386Builds with and without dead-flag elision: the
// listings differ only where a MOV and an ADD, SUB, INC or DEC became one
// LEA into the same register, or an AND that zero-extends a register, after
// a MOV for a narrowing, became MOVZX, and on 386 both compute the same
// results.
func TestI386DeadFlagElisionOnlySelectsLEA(t *testing.T) {
	type kernel struct {
		name  string
		w     uint8
		build func(*n.Builder)
	}
	var kernels []kernel
	for _, c := range i386LEABuilds {
		kernels = append(kernels, kernel{c.name, c.w, func(b *n.Builder) { c.build(b, b.ParamBits(c.w), b.ParamBits(c.w)) }})
	}
	for _, c := range flag386Builds {
		// As TestI386FlagListings builds them, and with x live past the
		// arithmetic, so that it writes another register.
		for _, live := range []bool{false, true} {
			name := c.name
			if live {
				name += "/x"
			}
			kernels = append(kernels, kernel{name, c.w, func(b *n.Builder) {
				x, y := b.ParamBits(c.w), b.ParamBits(c.w)
				p, r := c.build(b, x, y)
				yes := b.NewLabel()
				b.Branch(p, yes)
				switch {
				case r == (n.Value{}) && live:
					b.Return(x)
					b.Bind(yes)
					b.Return(b.ConstBits(c.w, 2))
				case r == (n.Value{}):
					b.Return(b.ConstBits(c.w, 1))
					b.Bind(yes)
					b.Return(b.ConstBits(c.w, 2))
				case live:
					b.Return(b.Xor(r, x))
					b.Bind(yes)
					b.Return(r)
				default:
					b.Return(b.Xor(r, b.ConstBits(c.w, 0x5a)))
					b.Bind(yes)
					b.Return(r)
				}
			}})
		}
	}
	funcs := []compile.Func{
		{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}},
		{ABI: abi.C},
	}
	lea := regexp.MustCompile(`^lea (E\w+), `)
	twoAddress := regexp.MustCompile(`^(add|sub|inc|dec) (E\w+)\b`)
	movzx := regexp.MustCompile(`^movzx (E\w+), (\w)(\w)$`)
	masks := map[string]string{"L": "0xff", "X": "0xffff", "I": "0xffff", "P": "0xffff"}
	values := []uint64{0, 1, 2, 7, 0x7f, 0x80, 0xff, 0x7fffffff, 0x80000000, 0xffffffff, 0x100000000, 1 << 63, 1<<63 - 1, ^uint64(0)}
	spec := compile.TargetSpec{Architecture: "386"}
	keep, err := compile.KeepFlagWrites(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, err := compile.NewTarget(spec)
	if err != nil {
		t.Fatal(err)
	}
	selected := 0
	for _, k := range kernels {
		for fi, f := range funcs {
			b := n.NewBuilder()
			k.build(b)
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			var units [2]*compile.Unit
			var rows [2][]string
			for i, tg := range []compile.Target{target, keep} {
				if units[i], err = compile.Lower(tg, p, f); err != nil {
					t.Fatal(err)
				}
				listing, err := units[i].Listing()
				if err != nil {
					t.Fatal(err)
				}
				rows[i] = i386LEARows(listing)
			}
			got, want := rows[0], rows[1]
			for i, j := 0, 0; i < len(got) || j < len(want); {
				if i < len(got) && j < len(want) && got[i] == want[j] {
					i, j = i+1, j+1
					continue
				}
				if m := movzx.FindStringSubmatch(i386Row(got, i)); m != nil {
					// MOVZX R, S8 or S16 for AND R, mask, after MOV R, S
					// unless S is R's own low part.
					and := "and " + m[1] + ", " + masks[m[3]]
					source := "E" + m[2] + m[3]
					if m[3] == "L" {
						source = "E" + m[2] + "X"
					}
					if i386Row(want, j) == and && source == m[1] {
						i, j, selected = i+1, j+1, selected+1
						continue
					}
					if i386Row(want, j) == "mov "+m[1]+", "+source && i386Row(want, j+1) == and {
						i, j, selected = i+1, j+2, selected+1
						continue
					}
				}
				if i < len(got) && j+1 < len(want) {
					if m := lea.FindStringSubmatch(got[i]); m != nil && strings.HasPrefix(want[j], "mov "+m[1]+", ") {
						if o := twoAddress.FindStringSubmatch(want[j+1]); o != nil && o[2] == m[1] {
							i, j, selected = i+1, j+2, selected+1
							continue
						}
					}
				}
				t.Fatalf("%s/f%d: row %d %q, without elision row %d %q:\n%s\nwithout elision:\n%s", k.name, fi, i, i386Row(got, i), j, i386Row(want, j), strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if runtime.GOARCH != "386" {
				continue
			}
			var codes [2]*execution.Code
			for i, u := range units {
				template, err := u.Template()
				if err != nil {
					t.Fatal(err)
				}
				if codes[i], err = template.Bind(nil); err != nil {
					t.Fatal(err)
				}
				defer codes[i].Close()
			}
			mask := ^uint64(0) >> (64 - k.w)
			for _, x := range values {
				for _, y := range values {
					x, y := x&mask, y&mask
					args := []uintptr{uintptr(x), uintptr(y)}
					if k.w == 64 {
						args = execution.Words(x, y)
					}
					var results [2]uint64
					for i, code := range codes {
						lo, hi, err := code.CallPair(args...)
						if err != nil {
							t.Fatal(err)
						}
						results[i] = execution.Wide(lo, hi)
					}
					if results[0] != results[1] {
						t.Fatalf("%s/f%d: f(%#x, %#x) = %#x, without elision %#x\n%s", k.name, fi, x, y, results[0], results[1], strings.Join(got, "\n"))
					}
				}
			}
		}
	}
	if selected == 0 {
		t.Error("no LEA or MOVZX selected")
	}
}

func i386Row(rows []string, i int) string {
	if i < len(rows) {
		return rows[i]
	}
	return "<end>"
}
