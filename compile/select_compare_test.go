package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestSelectOnCompareReadsFlags checks that a select on a single-use
// comparison compiles to the comparison and a flag-reading select, with no
// test of the 0/1 value, in both tiers.
func TestSelectOnCompareReadsFlags(t *testing.T) {
	for _, c := range []struct {
		arch, want string
		bits       uint8
	}{
		{"amd64", "cmov", 64}, {"386", "cmov", 32}, {"arm64", ", lt", 64}, {"ppc64le", "isel", 64},
	} {
		for _, tier := range []compile.Tier{compile.TierOptimized, compile.TierBaseline} {
			target, err := compile.NewTarget(compile.TargetSpec{Architecture: c.arch, Tier: tier})
			if err != nil {
				t.Fatal(err)
			}
			program, err := ir.Seal(&ir.Program{Arity: 2, Result: 3, Instructions: []ir.Instruction{
				{Op: ir.OpParam, Bits: c.bits}, {Op: ir.OpParam, Bits: c.bits},
				{Op: ir.OpCompare, Bits: c.bits, A: 0, B: 1, Imm: uint64(ir.BranchLess)},
				{Op: ir.OpSelect, Bits: c.bits, A: 2, B: 0, C: 1},
			}})
			if err != nil {
				t.Fatal(err)
			}
			unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
			if err != nil {
				t.Fatalf("%s/%d: %v", c.arch, tier, err)
			}
			listing, err := unit.Listing()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(listing, c.want) || strings.Contains(listing, "test ") || strings.Contains(listing, "cmpldi") || strings.Contains(listing, "#0") {
				t.Errorf("%s/%d: no flag-reading select:\n%s", c.arch, tier, listing)
			}
		}
	}
}
