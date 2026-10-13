package compile_test

import (
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestBackendInstructionBudgets(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "riscv64", "386", "ppc64le", "loong64"} {
		for _, op := range []ir.Opcode{ir.OpShiftRightBy, ir.OpRotateLeftBy, ir.OpMulHigh, ir.OpCarryAdd} {
			t.Run(fmt.Sprintf("%s/op%d", arch, op), func(t *testing.T) {
				w := uint8(64)
				budgets := map[string][4]int{"amd64": {8, 5, 6, 6}, "arm64": {5, 4, 3, 4}, "riscv64": {7, 8, 4, 5}, "386": {24, 18, 16, 15}, "ppc64le": {6, 3, 3, 5}, "loong64": {6, 4, 3, 4}}
				column := map[ir.Opcode]int{ir.OpShiftRightBy: 0, ir.OpRotateLeftBy: 1, ir.OpMulHigh: 2, ir.OpCarryAdd: 3}[op]
				budget := budgets[arch][column]
				if arch == "386" {
					w = 32
				}
				p := &ir.Program{Arity: 2, Result: 2, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: w}, {Op: ir.OpParam, Bits: w}, {Op: op, Bits: w, A: 0, B: 1}}}
				asm, err := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1024}})
				if err != nil {
					t.Fatal(err)
				}
				n := countAssemblyInstructions(asm)
				t.Logf("%d instructions", n)
				if n > budget {
					t.Fatalf("%d instructions exceeds %d:\n%s", n, budget, asm)
				}
				// TestBackendCapabilityMatrix inspects SelectedOpcode directly
				// to reject software fallback ladders, regardless of text syntax.
			})
		}
	}
}
