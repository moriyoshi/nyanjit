package machine_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

func TestPhysicalSizeCommonShape(t *testing.T) {
	// Assignment keeps the public helpers interchangeable without wrappers or
	// optional argument conventions. PPC64 exposes its configured Emitter.Size.
	for name, size := range map[string]func(machine.Instruction, abi.StackPractice, int) int{
		"amd64":   amd64.PhysicalSize,
		"arm64":   arm64.PhysicalSize,
		"386":     i386.PhysicalSize,
		"loong64": loong64.PhysicalSize,
		"riscv64": riscv64.PhysicalSize,
	} {
		t.Run(name, func(t *testing.T) {
			for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
				for _, homes := range []int{0, 9} {
					if got := size(machine.Instruction{Op: machine.OpReturn}, stack, homes); got <= 0 {
						t.Fatalf("stack=%v homes=%d: return size %d", stack, homes, got)
					}
				}
			}
		})
	}
}
