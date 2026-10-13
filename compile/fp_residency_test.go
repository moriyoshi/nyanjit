package compile_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestFPResidencyChainKeepsValuesInFPRegisters(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		b := nyanjit.NewBuilder()
		x := b.Param()
		y := x
		for i := 0; i < 10; i++ {
			y = b.FAdd(y, x)
		}
		b.Return(y)
		p, e := testFinishRaw(b)
		if e != nil {
			t.Fatal(e)
		}
		text, e := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{})
		if e != nil {
			t.Fatal(e)
		}
		transfers := 0
		for _, line := range strings.Split(text, "\n") {
			switch arch {
			case "386":
				if strings.Contains(line, "movd ") {
					transfers++
				}
			case "amd64":
				if strings.Contains(line, "movq") && strings.Contains(line, "xmm") {
					transfers++
				}
			case "arm64":
				if strings.Contains(line, "fmov") && strings.Contains(line, "X") {
					transfers++
				}
			case "loong64":
				if strings.Contains(line, "movgr2fr.") || strings.Contains(line, "movfr2gr.") {
					transfers++
				}
			case "riscv64":
				if strings.Contains(line, "fmv.d.x") || strings.Contains(line, "fmv.x.d") {
					transfers++
				}
			default:
				if strings.Contains(line, "mtvsrd") || strings.Contains(line, "mfvsrd") {
					transfers++
				}
			}
		}
		want := 2
		if arch == "386" {
			want = 4
		}
		if transfers != want {
			t.Fatalf("%s made%d bank transfers instead of entry/return only:\n%s", arch, transfers, text)
		}
	}
}
