package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

func TestCPUSizeRejectsForeignArchitecture(t *testing.T) {
	arm, err := (abi.CPUProfile{}).Resolved("arm64")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []func(){
		func() { amd64.NewByteAssembler(abi.StackPlatform, 0).WithCPU(arm) },
		func() { amd64.PhysicalSizeWithCPU(machine.Instruction{}, abi.StackPlatform, arm, 0) },
		func() { riscv64.NewByteAssembler(abi.StackPlatform, 0).WithCPU(arm) },
		func() { riscv64.PhysicalSizeWithCPU(machine.Instruction{}, abi.StackPlatform, arm, 0) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("foreign CPU accepted")
				}
			}()
			f()
		}()
	}
}
