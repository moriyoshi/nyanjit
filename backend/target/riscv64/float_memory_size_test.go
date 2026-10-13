package riscv64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// TestFloatMemorySizeMatchesEncoding checks the assembler-free measure
// against the encoding for near and far slots, both stack practices and
// rejected layouts.
func TestFloatMemorySizeMatchesEncoding(t *testing.T) {
	for _, op := range []machine.Opcode{machine.OpFloatSpill, machine.OpFloatReload} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform, abi.StackPractice(99)} {
			for _, homes := range []int{0, 10, 4094, 4095} {
				for _, slot := range []int{0, 1, 255, 256, 4000, 1 << 20, 1 << 26} {
					ins := machine.Instruction{Op: op, A: backend.VectorRegister(3), Slot: slot}
					want := 0
					if validInstructionRegisters(&ins) && validSlot(slot, homes) {
						a := NewByteAssembler(stack, homes)
						a.FloatMemory(ins)
						want = len(a.words) * 4
					}
					if got := FloatMemorySize(ins, stack, homes); got != want {
						t.Errorf("op%d stack %d homes %d slot %d: size %d, encoding %d", op, stack, homes, slot, got, want)
					}
				}
			}
		}
	}
	ins := machine.Instruction{Op: machine.OpFloatReload, A: backend.VectorRegister(3), Slot: 4}
	if n := testing.AllocsPerRun(100, func() { FloatMemorySize(ins, abi.StackPlatform, 10) }); n != 0 {
		t.Fatalf("float reload size allocates %g", n)
	}
}
