package riscv64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func TestZeroRotateUsesMoveOrNoInstruction(t *testing.T) {
	for _, test := range []struct {
		dst, src machine.Register
		want     int
	}{{10, 10, 0}, {11, 10, 4}} {
		for _, operation := range []ir.Opcode{ir.OpRotateLeft, ir.OpRotateRight} {
			ins := machine.Instruction{Op: machine.OpShift, Arithmetic: operation, Bits: 64, A: test.dst, B: test.src}
			a := NewByteAssembler(abi.StackNoUse, 0)
			a.Shift(operation, 64, test.dst, test.src, 0)
			if got := a.AssembledSize(); got != test.want {
				t.Fatalf("rotate %d x%d, x%d emitted %d bytes, want %d", operation, test.dst, test.src, got, test.want)
			}
			if got := PhysicalSize(ins, abi.StackNoUse, 0); got != test.want {
				t.Fatalf("rotate %d x%d, x%d sized %d bytes, want %d", operation, test.dst, test.src, got, test.want)
			}
		}
	}
}
