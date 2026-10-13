package compile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// switchProgram switches a w-bit selector over n cases, of which the
// targets function gives each one's target among targets labels.
func switchProgram(t *testing.T, w uint8, n, targets int, target func(int) int) *ir.SealedProgram {
	t.Helper()
	b := nyanjit.NewBuilder()
	selector, x := b.ParamBits(w), b.Param()
	labels := make([]nyanjit.Label, targets)
	for i := range labels {
		labels[i] = b.NewLabel()
	}
	otherwise := b.NewLabel()
	list := make([]nyanjit.Label, n)
	for i := range list {
		list[i] = labels[target(i)]
	}
	b.Switch(selector, list, otherwise)
	for i, l := range labels {
		b.Bind(l)
		b.Return(b.Add(x, b.Const(uint64(3*i+1))))
	}
	b.Bind(otherwise)
	b.Return(x)
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return program
}

// switchSequences are, per architecture, a mnemonic of the jump table's
// index clamp and one of its indirect jump or its table address.
var switchSequences = map[string][2]string{
	"amd64":   {"cmovb ", "notrack jmp "},
	"386":     {"cmovb ", "jmp E"},
	"arm64":   {"csel ", "br X16"},
	"riscv64": {"sltu ", "addi x30, x30, 28"},
	"loong64": {"masknez ", "pcaddi r19, 5"},
	"ppc64le": {"isel ", "bctr\n"},
}

func switchListing(t *testing.T, arch string, tier compile.Tier, harden bool, program *ir.SealedProgram) (string, error) {
	t.Helper()
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier})
	if err != nil {
		t.Fatal(err)
	}
	unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C, Harden: harden})
	if err != nil {
		return "", err
	}
	return unit.Listing()
}

// A dense switch of many ranges becomes a jump table whose index is clamped
// without a branch; Harden and the baseline tier keep compare trees, so
// they emit no indirect jump.
func TestSwitchJumpTableListings(t *testing.T) {
	dense := switchProgram(t, 32, 16, 16, func(i int) int { return i })
	for arch, sequence := range switchSequences {
		t.Run(arch, func(t *testing.T) {
			listing, err := switchListing(t, arch, compile.TierOptimized, false, dense)
			if err != nil {
				t.Fatal(err)
			}
			clamp := strings.Index(listing, sequence[0])
			jump := strings.Index(listing, sequence[1])
			if clamp < 0 || jump < clamp {
				t.Fatalf("no clamped jump table (%q before %q):\n%s", sequence[0], sequence[1], listing)
			}
			for _, harden := range []bool{false, true} {
				for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
					if tier == compile.TierOptimized && !harden {
						continue
					}
					listing, err := switchListing(t, arch, tier, harden, dense)
					if harden && errors.Is(err, compile.ErrUnsupported) && (arch == "riscv64" || arch == "loong64") {
						continue // no speculation barrier for hardened control flow
					}
					if err != nil {
						t.Fatalf("tier %d harden %v: %v", tier, harden, err)
					}
					if strings.Contains(listing, sequence[1]) {
						t.Fatalf("tier %d harden %v has an indirect jump:\n%s", tier, harden, listing)
					}
				}
			}
		})
	}
}

// Below the thresholds, and for a 64-bit selector on 386, the optimized
// tier keeps a compare tree.
func TestSwitchJumpTableThresholds(t *testing.T) {
	cases := []struct {
		name    string
		program *ir.SealedProgram
		arch    string
		table   bool
	}{
		{"seven-ranges", switchProgram(t, 64, 7, 7, func(i int) int { return i }), "arm64", false},
		{"eight-ranges", switchProgram(t, 64, 8, 8, func(i int) int { return i }), "arm64", true},
		{"sparse-ranges", switchProgram(t, 64, 100, 8, func(i int) int { return i / 13 }), "arm64", false},
		{"duplicates", switchProgram(t, 64, 20, 2, func(i int) int { return i % 2 }), "arm64", true},
		{"386-wide", switchProgram(t, 64, 16, 16, func(i int) int { return i }), "386", false},
		{"386-narrow", switchProgram(t, 16, 16, 16, func(i int) int { return i }), "386", true},
	}
	for _, c := range cases {
		listing, err := switchListing(t, c.arch, compile.TierOptimized, false, c.program)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := strings.Contains(listing, switchSequences[c.arch][1]); got != c.table {
			t.Errorf("%s: jump table %v, want %v:\n%s", c.name, got, c.table, listing)
		}
	}
}
