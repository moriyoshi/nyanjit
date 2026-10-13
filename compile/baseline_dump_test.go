//go:build linux && (arm64 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"os"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestBaselineDump(t *testing.T) {
	name := os.Getenv("BASELINE_DUMP")
	if name == "" {
		t.Skip()
	}
	for _, k := range baselineKernels {
		if k.name != name {
			continue
		}
		b := nyanjit.NewBuilder()
		k.build(b)
		p, _ := b.Finish()
		for _, tier := range []compile.Tier{compile.TierBaseline, compile.TierOptimized} {
			target, _ := compile.NewTarget(compile.TargetSpec{Tier: tier})
			unit, err := compile.Lower(target, p, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}})
			if err != nil {
				t.Fatal(err)
			}
			l, err := unit.Listing()
			t.Logf("tier %d err=%v report=%+v\n%s", tier, err, unit.Report(), l)
		}
	}
}
