package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestPhysicalRegisterLookupMatchesPolicies(t *testing.T) {
	for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "loong64", "ppc64le"} {
		for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
			for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
				target := newMachineTarget(mustTarget(t, TargetSpec{Architecture: arch}).options(Func{ABI: flavor, Stack: StackPolicy{Practice: stack}}))
				var buffer [64]physReg
				for _, register := range target.PhysicalRegisters().AppendTo(buffer[:0]) {
					name := target.RegisterName(register)
					if got, ok := physicalRegistersByArchitecture[arch][name]; !ok || got != register {
						t.Fatalf("%s %v %v %s: got %d/%t, want %d", arch, flavor, stack, name, got, ok, register)
					}
				}
			}
		}
	}
}
