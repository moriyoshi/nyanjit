package amd64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func TestBinaryImmediatePhysicalSize(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, value := range []uint64{0, 127, 128, 255, 0xffff, 0xffffffff, ^uint64(127), ^uint64(128), 0x7fffffff, ^uint64(0x7fffffff)} {
			for _, destination := range []machine.Register{0, 8, 9} {
				for _, source := range []machine.Register{0, 8, 9} {
					ins := machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: ir.OpXor,
						Bits: width, A: destination, B: source, Imm: value}
					bytes := NewByteAssembler(abi.StackNoUse, 0)
					if err := machine.Visit(ins, bytes); err != nil {
						t.Fatal(err)
					}
					text := NewTextAssembler(abi.StackNoUse, 0)
					if err := machine.Visit(ins, text); err != nil {
						t.Fatal(err)
					}
					want := bytes.AssembledSize()
					if got := PhysicalSize(ins, abi.StackNoUse); got != want {
						t.Errorf("value %#x, dst %d, src %d: physical size %d, encoded size %d", value, destination, source, got, want)
					}
					if got := text.AssembledSize(); got != want {
						t.Errorf("value %#x, dst %d, src %d: text size %d, encoded size %d", value, destination, source, got, want)
					}
				}
			}
		}
	}
}
