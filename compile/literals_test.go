package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestFloatLiteralPoolListings(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		for _, w := range []uint8{32, 64} {
			b := nyanjit.NewBuilder()
			x := b.ParamBits(w)
			c := b.Float64(1.25)
			if w == 32 {
				c = b.Float32(1.25)
			}
			b.Return(b.FAdd(b.FMul(x, c), c))
			p, e := testFinishRaw(b)
			if e != nil {
				t.Fatal(e)
			}
			text, e := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{})
			if e != nil {
				t.Fatal(e)
			}
			want := map[string]string{"amd64": "[rip +", "386": "[literal +", "arm64": "# literal", "riscv64": "auipc x31", "loong64": "pcaddu12i r19", "ppc64le": "lfd f"}[arch]
			if w == 32 && arch == "ppc64le" {
				want = "lfs f"
			}
			if !strings.Contains(text, want) || !strings.Contains(text, ".byte") {
				t.Fatalf("%s%d missing pool load:\n%s", arch, w, text)
			}
		}
	}
}
