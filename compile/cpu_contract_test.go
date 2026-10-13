package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

func TestAssemblersRejectForeignCPUContracts(t *testing.T) {
	am, err := (abi.CPUProfile{}).Resolved("amd64")
	if err != nil {
		t.Fatal(err)
	}
	rv, err := (abi.CPUProfile{}).Resolved("riscv64")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []func(){
		func() { amd64.NewByteAssembler(abi.StackPlatform, 0).WithCPU(rv) },
		func() { amd64.NewTextAssembler(abi.StackPlatform, 0).WithCPU(rv) },
		func() { riscv64.NewByteAssembler(abi.StackPlatform, 0).WithCPU(am) },
		func() { riscv64.NewTextAssembler(abi.StackPlatform, 0).WithCPU(am) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("foreign CPU contract accepted")
				}
			}()
			call()
		}()
	}
	amd64.NewByteAssembler(abi.StackPlatform, 0).WithCPU(abi.ResolvedCPU{})
	riscv64.NewByteAssembler(abi.StackPlatform, 0).WithCPU(rv)
}
