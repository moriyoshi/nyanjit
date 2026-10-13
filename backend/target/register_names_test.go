package target_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

// TestPolicyRegisterSetsCarryNames checks that every register set a policy
// returns names its registers, and any other register, as RegisterName does.
func TestPolicyRegisterSetsCarryNames(t *testing.T) {
	type policy interface {
		Registers() backend.RegisterSet
		PhysicalRegisters() backend.RegisterSet
		RegisterName(backend.Register) string
	}
	policies := map[string][]policy{"ppc64": {ppc64.Policy{}}}
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			policies["amd64"] = append(policies["amd64"], amd64.NewPolicy(flavor, stack))
			policies["arm64"] = append(policies["arm64"], arm64.NewPolicy(flavor, stack))
			policies["386"] = append(policies["386"], i386.NewPolicy(flavor, stack))
			policies["loong64"] = append(policies["loong64"], loong64.NewPolicy(flavor, stack))
			policies["riscv64"] = append(policies["riscv64"], riscv64.NewPolicy(flavor, stack))
		}
	}
	for arch, list := range policies {
		for _, p := range list {
			for name, set := range map[string]backend.RegisterSet{"Registers": p.Registers(), "PhysicalRegisters": p.PhysicalRegisters()} {
				if !set.Named() {
					t.Errorf("%s %s carries no register names", arch, name)
					continue
				}
				for i := range set.Len() {
					if got, want := set.Name(i), p.RegisterName(set.At(i)); got != want || got == "" {
						t.Errorf("%s %s[%d] named %q, want %q", arch, name, i, got, want)
					}
				}
				// One table names every register, in and out of the set.
				for r := range 256 {
					if got, want := set.NameOf(backend.Register(r)), p.RegisterName(backend.Register(r)); got != want {
						t.Errorf("%s %s names register %d %q, RegisterName %q", arch, name, r, got, want)
					}
				}
			}
		}
	}
}
