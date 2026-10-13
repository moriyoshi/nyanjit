package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestParameterRegisterBanks(t *testing.T) {
	for _, architecture := range []string{"amd64", "386", "arm64", "riscv64", "loong64", "ppc64le"} {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			target := newMachineTarget(mustTarget(t, TargetSpec{Architecture: architecture}).options(Func{ABI: flavor}))
			count := 8
			switch architecture {
			case "386":
				count = 0
			case "amd64":
				count = 6
				if flavor == abi.Go {
					count = 9
				}
			case "arm64", "loong64", "riscv64":
				if flavor == abi.Go {
					count = 16
				}
			case "ppc64le":
				if flavor == abi.Go {
					count = 12
				}
			}
			for i := -1; i <= 32; i++ {
				r, ok := target.ParamRegister(i)
				if ok != (i >= 0 && i < count) {
					t.Fatalf("%s/%v parameter %d: %v", architecture, flavor, i, ok)
				}
				if ok && !target.PhysicalRegisters().Contains(r) {
					t.Fatalf("%s parameter %d: nonphysical register %d", architecture, i, r)
				}
			}
		}
	}
}
