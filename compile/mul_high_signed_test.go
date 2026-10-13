package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

// Each target selects its native signed high product; 386 expands only the
// widths other than 32 bits.
func TestMulHighSignedNativeForms(t *testing.T) {
	forms := map[string]map[uint8]string{
		"amd64":   {8: "R11", 16: "R11", 32: "R11", 64: "imul R"},
		"386":     {32: "imul E"},
		"arm64":   {8: "sxtb", 16: "sxth", 32: "smull", 64: "smulh"},
		"riscv64": {8: "mulh ", 16: "mulh ", 32: "mulh ", 64: "mulh "},
		"ppc64le": {8: "extsb", 16: "extsh", 32: "mulhw", 64: "mulhd"},
		"loong64": {8: "mulh.d", 16: "mulh.d", 32: "mulh.w", 64: "mulh.d"},
	}
	for arch, widths := range forms {
		for _, w := range []uint8{8, 16, 32, 64} {
			b := nyanjit.NewBuilder()
			x, y := b.ParamBits(w), b.ParamBits(w)
			b.Return(b.MulHighSigned(x, y))
			listing, err := lowerListing(finishCrossAssembly(t, b), compile.TargetSpec{Architecture: arch}, compile.Func{})
			if err != nil {
				t.Fatalf("%s %d bits: %v", arch, w, err)
			}
			want, native := widths[w]
			if native && !strings.Contains(listing, want) {
				t.Errorf("%s %d bits: no %q in\n%s", arch, w, want, listing)
			}
		}
	}
}
