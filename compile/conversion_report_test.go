package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/compile"
)

// Conversions raise inexact and invalid, so a body whose only floating
// operation is a conversion still reports FP status modification.
func TestConversionReportsFPStatus(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "ppc64le", "loong64"} {
		for _, toInteger := range []bool{false, true} {
			b := nyanjit.NewBuilder()
			x := b.ParamBits(32)
			if toInteger {
				b.Return(b.FToSigned(x, 32))
			} else {
				b.Return(b.FFromSigned(x, 32))
			}
			report, err := lowerReport(finishCrossAssembly(t, b), compile.TargetSpec{Architecture: arch}, compile.Func{})
			if err != nil {
				t.Fatalf("%s: %v", arch, err)
			}
			if !report.FPStatusModified() {
				t.Fatalf("%s to-integer=%t: FP status not reported", arch, toInteger)
			}
		}
	}
}
