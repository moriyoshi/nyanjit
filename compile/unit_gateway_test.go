package compile

import (
	"errors"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestCrossTargetUnitsRejectHostRuntimeGateways(t *testing.T) {
	p := &ir.Program{Result: 1, Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 64, Imm: 0x1234}, {Op: ir.OpCallC, Bits: 64, Args: []int{0}}}}
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		if runtime.GOOS == "linux" && runtime.GOARCH == arch {
			continue
		}
		spec := TargetSpec{Architecture: arch}
		f := Func{Context: ContextGoManaged, AssumePointerFree: true, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
		// Cross-target managed C calls lower; only the concrete listing needs
		// the target host's gateways.
		gateway, err := lowerUnit(p, spec, f)
		if err != nil {
			t.Fatalf("%s cross gateway lowering: %v", arch, err)
		}
		if _, err := gateway.Listing(); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s cross gateway: %v", arch, err)
		}
		if report := gateway.Report(); report.Architecture() != arch || !report.HasFunctionCalls() {
			t.Fatalf("%s cross gateway report: %+v", arch, report.Snapshot())
		}
		listing, err := gateway.ListingSymbolic()
		if err != nil || listing == "" {
			t.Fatalf("symbolic %s listing: %v", arch, err)
		}
		pure := &ir.Program{Result: 0, Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 32, Imm: 7}}}
		u, err := lowerUnit(pure, spec, f)
		if err != nil {
			t.Fatalf("%s gateway-free cross lowering: %v", arch, err)
		}
		if _, err := u.Listing(); err != nil {
			t.Fatal(err)
		}
	}
}
