//go:build linux && riscv64

package riscv64_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/target/riscv64"
)

func TestFarBranchesExecute(t *testing.T) {
	const targetPC = 1<<20 + 8192
	for _, conditional := range []bool{false, true} {
		name := "unconditional"
		if conditional {
			name = "conditional"
		}
		t.Run(name, func(t *testing.T) {
			a := riscv64.NewByteAssembler(abi.StackNoUse, 0)
			a.SetPC(0)
			if conditional {
				a.CompareImmediate(10, 0)
				a.JumpIfRange(ir.BranchEqual, targetPC, true, true)
			} else {
				a.JumpForm(targetPC, true)
			}
			a.Constant(10, 7)
			a.Ret()
			for a.AssembledSize() < targetPC {
				a.Constant(0, 0) // ADDI x0, x0, 0 is a NOP.
			}
			if got := a.AssembledSize(); got != targetPC {
				t.Fatalf("target PC = %d, want %d", got, targetPC)
			}
			a.Constant(10, 42)
			a.Ret()
			code, err := execution.NewCode(a.Bytes(), execution.CodeOptions{ABI: abi.C, Arity: 1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, input := range []uint64{0, 1} {
				want := uint64(42)
				if conditional && input != 0 {
					want = 7
				}
				got, err := code.Call(uintptr(input))
				if err != nil || uint64(got) != want {
					t.Fatalf("branch(%d) = %d, %v; want %d", input, got, err, want)
				}
			}
		})
	}
}
