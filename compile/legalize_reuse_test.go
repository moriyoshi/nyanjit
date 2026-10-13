package compile_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// TestLegalizedProgramsOwnTheirCode checks that a unit keeps nothing of the
// buffer vector legalization builds in: a later legalization of another
// program, which reuses that buffer, leaves the unit's listing unchanged.
func TestLegalizedProgramsOwnTheirCode(t *testing.T) {
	first, err := carrylessMulChain(1)
	if err != nil {
		t.Fatal(err)
	}
	other, err := carrylessMulChain(3)
	if err != nil {
		t.Fatal(err)
	}
	f := compile.Func{ABI: abi.C, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 20}}
	for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "ppc64le", "loong64"} {
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch, Tier: tier})
			if err != nil {
				t.Fatal(err)
			}
			unit, err := compile.Lower(target, first, f)
			if errors.Is(err, compile.ErrUnsupported) {
				continue
			}
			if err != nil {
				t.Fatalf("%s tier %d: %v", arch, tier, err)
			}
			if _, err := compile.Lower(target, other, f); err != nil {
				t.Fatalf("%s tier %d: other program: %v", arch, tier, err)
			}
			again, err := compile.Lower(target, first, f)
			if err != nil {
				t.Fatal(err)
			}
			want, err := again.Listing()
			if err != nil {
				t.Fatal(err)
			}
			got, err := unit.Listing()
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("%s tier %d: listing changed after another legalization", arch, tier)
			}
		}
	}
}
