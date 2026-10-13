package compile_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

var hardenArchitectures = []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"}

// hardenedBoundsCheck is the cross-target fixture of the optimized tier's
// hardening tests: a checked byte load.
func hardenedBoundsCheck(t *testing.T) *ir.SealedProgram {
	t.Helper()
	b := nyanjit.NewBuilder()
	base, index, count := b.ParamNative(), b.ParamNative(), b.ParamNative()
	bad := b.NewLabel()
	b.Return(b.LoadBits(b.IndexedAddress(base, index, count, 1, bad), 8))
	b.Bind(bad)
	b.Return(b.ConstBits(8, 0))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Each target with a speculation barrier starts every hardened label of a
// baseline function with it and clamps the checked index on the success
// edge; targets without one reject control flow, as the optimized tier does.
func TestBaselineHardenControlPerTarget(t *testing.T) {
	if builder.NativeBits != 64 {
		t.Skip("cross-target fixture uses 64-bit builder addresses")
	}
	p := hardenedBoundsCheck(t)
	f := compile.Func{Context: compile.ContextExternal, Harden: true, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}}
	for _, c := range []struct{ arch, barrier, clamp string }{
		{"amd64", "lfence", `\.byte 4[89a-f] 0f 43 `}, // CMOVAE r64, r64
		{"386", "lfence", `\.byte 0f 43 `},            // CMOVAE r32, r32
		{"arm64", "dsb sy", `csel (X\d+), (X\d+), X\d+, lo`},
		{"ppc64le", "ori r31, r31, 0", `isel r\d+, r\d+, r\d+, 0`},
		{"riscv64", "", ""},
		{"loong64", "", ""},
	} {
		spec := compile.TargetSpec{Architecture: c.arch, Tier: compile.TierBaseline}
		listing, err := lowerListing(p, spec, f)
		_, optimizedErr := lowerListing(p, compile.TargetSpec{Architecture: c.arch}, f)
		if c.barrier == "" {
			if !errors.Is(err, compile.ErrUnsupported) || !errors.Is(optimizedErr, compile.ErrUnsupported) {
				t.Errorf("%s: hardened control flow accepted without a speculation barrier: baseline %v, optimized %v", c.arch, err, optimizedErr)
			}
			continue
		}
		if err != nil || optimizedErr != nil {
			t.Fatalf("%s: baseline %v, optimized %v", c.arch, err, optimizedErr)
		}
		// The success edge, the in-bounds block and the failure block.
		if n := strings.Count(listing, c.barrier); n < 3 || !regexp.MustCompile(c.clamp).MatchString(listing) {
			t.Errorf("%s: %d barriers, clamp %v:\n%s", c.arch, n, regexp.MustCompile(c.clamp).MatchString(listing), listing)
		}
		// The clamp follows the barrier that starts its edge.
		at := regexp.MustCompile(c.clamp).FindStringIndex(listing)[0]
		if b := strings.Index(listing, c.barrier); b < 0 || b > at {
			t.Errorf("%s: clamp before the first barrier:\n%s", c.arch, listing)
		}
	}
}

// Straight-line hardened functions need no barrier: every target accepts
// them at both tiers.
func TestBaselineHardenStraightLineEveryTarget(t *testing.T) {
	p := finished(t, func(b *nyanjit.Builder) {
		x := b.Param()
		b.Return(b.Add(b.RotateLeft(b.Xor(x, b.Const(0x3141592653589793)), 17), b.Const(0x2718281828459045)))
	})
	for _, arch := range hardenArchitectures {
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			if _, err := lowerListing(p, compile.TargetSpec{Architecture: arch, Tier: tier}, compile.Func{ABI: abi.C, Harden: true}); err != nil {
				t.Errorf("%s tier %d: %v", arch, tier, err)
			}
		}
	}
}

// Every hardened lowering draws fresh keys; unhardened lowerings repeat.
func TestBaselineHardenRandomizesEachLowering(t *testing.T) {
	p := finished(t, func(b *nyanjit.Builder) { b.Return(b.Add(b.Param(), b.Const(7))) })
	for _, arch := range hardenArchitectures {
		spec := compile.TargetSpec{Architecture: arch, Tier: compile.TierBaseline}
		for _, harden := range []bool{false, true} {
			f := compile.Func{ABI: abi.C, Harden: harden}
			a, errA := lowerListing(p, spec, f)
			b, errB := lowerListing(p, spec, f)
			if errA != nil || errB != nil {
				t.Fatal(errA, errB)
			}
			if (a == b) == harden {
				t.Errorf("%s harden %v: listings equal %v:\n%s\n%s", arch, harden, a == b, a, b)
			}
		}
	}
}

// The clamp writes the C argument register that carries the index, and the
// report says so, as TestHardenControlReportIncludesClampedArgument checks for
// the optimized tier.
func TestBaselineHardenReportIncludesClampedArgument(t *testing.T) {
	if builder.NativeBits != 64 {
		t.Skip("cross-target fixture uses 64-bit builder addresses")
	}
	p := hardenedBoundsCheck(t)
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}, Harden: true}
	report, err := lowerReport(p, compile.TargetSpec{Architecture: "amd64", Tier: compile.TierBaseline}, f)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, register := range report.ClobberedRegisters() {
		found = found || register == "RSI"
	}
	if !found {
		t.Errorf("clamped C argument RSI missing from clobbers: %v", report.ClobberedRegisters())
	}
}
