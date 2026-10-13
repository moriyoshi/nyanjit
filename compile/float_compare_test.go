package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// Every target compares floats natively, for every condition and width,
// both as a value and as a branch predicate.
func TestFloatCompareNativeForms(t *testing.T) {
	forms := map[string]string{
		"amd64":   "ucomis",
		"386":     "ucomis",
		"arm64":   "fcmp",
		"riscv64": "f",
		"ppc64le": "fcmpu",
		"loong64": "fcmp.",
	}
	for arch, want := range forms {
		for _, w := range []uint8{32, 64} {
			for c := ir.FloatEqual; c <= ir.FloatUnorderedEqual; c++ {
				for _, branch := range []bool{false, true} {
					b := nyanjit.NewBuilder()
					x, y := b.ParamBits(w), b.ParamBits(w)
					if branch {
						yes := b.NewLabel()
						b.Branch(b.FComparePredicate(x, c, y), yes)
						b.Return(b.Const(0))
						b.Bind(yes)
						b.Return(b.Const(1))
					} else {
						b.Return(b.FCompare(x, c, y))
					}
					listing, err := lowerListing(finishCrossAssembly(t, b), compile.TargetSpec{Architecture: arch}, compile.Func{})
					if err != nil {
						t.Errorf("%s %d bits, condition %d, branch %v: %v", arch, w, c, branch, err)
						continue
					}
					if !strings.Contains(listing, want) {
						t.Errorf("%s %d bits, condition %d: no %q in\n%s", arch, w, c, want, listing)
					}
				}
			}
		}
	}
}
