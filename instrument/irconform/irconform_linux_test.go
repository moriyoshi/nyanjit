//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386 || loong64)

package irconform_test

import (
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/irconform"
)

var baseline = map[string]string{
	"amd64": "x86-64-v1", "386": "i686", "arm64": "armv8.0-a", "loong64": "la64v1.0", "ppc64le": "power8", "riscv64": "rv64gc",
}

// TestConformance runs the corpus on the host CPU, with vectors lowered to
// scalars, and on the architecture's baseline CPU.
func TestConformance(t *testing.T) {
	specs := []struct {
		name string
		spec compile.TargetSpec
	}{
		{"host", compile.TargetSpec{}},
		{"vectors-off", compile.TargetSpec{Vectors: compile.VectorsOff()}},
		{"baseline", compile.TargetSpec{CPU: abi.CPUProfile{Level: baseline[runtime.GOARCH]}}},
		{"tier-baseline", compile.TargetSpec{Tier: compile.TierBaseline}},
		{"tier-baseline-cpu", compile.TargetSpec{Tier: compile.TierBaseline, CPU: abi.CPUProfile{Level: baseline[runtime.GOARCH]}}},
	}
	cases := irconform.Cases()
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			target, err := compile.NewTarget(s.spec)
			if err != nil {
				t.Skipf("%s: %v", s.name, err)
			}
			var pass, skip, gap, fail int
			for _, c := range cases {
				var gapped bool
				ok := t.Run(c.Name, func(t *testing.T) {
					defer func() { gapped = t.Skipped() }()
					irconform.Check(t, target, c)
				})
				switch {
				case !ok:
					fail++
				case gapped && s.spec.Tier == compile.TierBaseline:
					gap++
				case gapped:
					skip++
				default:
					pass++
				}
			}
			t.Logf("%s: %d pass, %d skip, %d baseline gaps, %d fail", s.name, pass, skip, gap, fail)
		})
	}
}

func TestCasesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range irconform.Cases() {
		if seen[c.Name] || len(c.Inputs) == 0 {
			t.Fatalf("duplicate or empty case %q", c.Name)
		}
		seen[c.Name] = true
	}
	t.Logf("%d cases", len(seen))
}
