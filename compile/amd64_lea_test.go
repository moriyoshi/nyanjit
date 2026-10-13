package compile_test

import (
	"runtime"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// An amd64 addition into a register other than its first operand uses LEA
// unless a later instruction reads its flags.
func TestAMD64AdditionWithDeadFlagsUsesLEA(t *testing.T) {
	build := func(f func(*n.Builder, n.Value, n.Value)) *ir.SealedProgram {
		b := n.NewBuilder()
		f(b, b.ParamBits(64), b.ParamBits(64))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		name    string
		p       *ir.SealedProgram
		listing string
		calls   [][3]uintptr // x, y, result
	}{
		{"straight", build(func(b *n.Builder, x, y n.Value) {
			b.Return(b.Add(b.Add(x, b.ConstBits(64, 7)), b.Mul(x, y)))
		}), "lea RAX, [RDI + 7]\nimul RDI, RSI\nadd RAX, RDI\nret", [][3]uintptr{{2, 3, 15}, {^uintptr(0), 1, 5}}},
		// The branch reads the addition's zero flag, so ADD stays, though
		// x lives on and the sum goes to another register. Allocators
		// differ after the branch; the listing is checked up to it.
		{"flags read", build(func(b *n.Builder, x, y n.Value) {
			s := b.Add(x, y)
			zero := b.NewLabel()
			b.Branch(b.IsZero(s), zero)
			b.Return(b.Xor(b.Add(s, b.ConstBits(64, 5)), x))
			b.Bind(zero)
			b.Return(b.ConstBits(64, 99))
		}), "", [][3]uintptr{{2, 3, 8}, {1, ^uintptr(0), 99}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, f := compile.TargetSpec{Architecture: "amd64"}, compile.Func{ABI: abi.C}
			listing, err := lowerListing(tc.p, spec, f)
			if err != nil {
				t.Fatal(err)
			}
			var rows []string
			for _, line := range strings.Split(listing, "\n") {
				if _, instruction, ok := strings.Cut(line, "  "); ok {
					rows = append(rows, instruction)
				}
			}
			if tc.listing == "" {
				// A copy of x and a flag-writing ADD feed the branch.
				sum, ok := strings.CutPrefix(rows[0], "mov ")
				sum, copied := strings.CutSuffix(sum, ", RDI")
				if !ok || !copied || sum == "RDI" || len(rows) < 3 || rows[1] != "add "+sum+", RSI" || !strings.HasPrefix(rows[2], "j") {
					t.Errorf("listing:\n%s\nwant a copy of RDI, ADD of RSI into it, then the branch", strings.Join(rows, "\n"))
				}
			} else if got := strings.Join(rows, "\n"); got != tc.listing {
				t.Errorf("listing:\n%s\nwant:\n%s", got, tc.listing)
			}
			if runtime.GOARCH != "amd64" {
				return
			}
			target, err := compile.NewTarget(spec)
			if err != nil {
				t.Fatal(err)
			}
			unit, err := compile.Lower(target, tc.p, compile.Func{ABI: abi.C, Context: compile.ContextForeign,
				Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
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
			defer code.Close()
			for _, c := range tc.calls {
				if got, err := code.Call(c[0], c[1]); err != nil || got != c[2] {
					t.Errorf("f(%#x, %#x) = %#x, %v; want %#x", c[0], c[1], got, err, c[2])
				}
			}
		})
	}
}
