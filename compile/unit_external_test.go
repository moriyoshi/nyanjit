package compile_test

import (
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestUnitListingTemplateOffsets(t *testing.T) {
	workloads := []struct {
		name string
		p    *ir.SealedProgram
	}{
		{"integer", workload.Chain(20)}, {"floating", workload.FP(20)},
		{"control", workload.Loops(40)}, {"vectors", workload.Vectors(12)},
	}
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		for _, w := range workloads {
			t.Run(arch+"/"+w.name, func(t *testing.T) {
				u, err := lowerUnit(w.p, compile.TargetSpec{Architecture: arch}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
				if err != nil {
					t.Fatal(err)
				}
				compile.AssertUnitOffsets(t, u)
			})
		}
	}
}

func TestUnitReportListingAreLazyAndDetached(t *testing.T) {
	compile.CheckUnitReportListingAreLazyAndDetached(t, workload.Chain(40).Clone())
}

func TestUnitConcurrentOutputs(t *testing.T) {
	if runtime.GOOS != "linux" || !compile.HostBackend() {
		t.Skip("no native backend")
	}
	compile.CheckUnitConcurrentOutputs(t, workload.Chain(40))
}
