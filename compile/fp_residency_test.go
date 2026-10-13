package compile_test

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"strings"
	"testing"
)

func TestFPResidencyChainKeepsValuesInFPRegisters(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "ppc64", "ppc64le"} {
		b := nyanjit.NewBuilder()
		x := b.Param()
		y := x
		for i := 0; i < 10; i++ {
			y = b.FAdd(y, x)
		}
		b.Return(y)
		p, e := b.Finish()
		if e != nil {
			t.Fatal(e)
		}
		text, e := compile.EmitAssembly(p, abi.CompileOptions{Architecture: arch})
		if e != nil {
			t.Fatal(e)
		}
		transfers := 0
		for _, line := range strings.Split(text, "\n") {
			switch arch {
			case "amd64":
				if strings.Contains(line, "movq") && strings.Contains(line, "xmm") {
					transfers++
				}
			case "arm64":
				if strings.Contains(line, "fmov") && strings.Contains(line, "X") {
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
		if transfers != 2 {
			t.Fatalf("%s made%d bank transfers instead of entry/return only:\n%s", arch, transfers, text)
		}
	}
}
