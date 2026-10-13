package compile_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/ppc64"
	"github.com/moriyoshi/nyanjit/target/riscv64"
)

func TestLiteralOffsetOverflowReview(t *testing.T) {
	for _, disp := range []uint64{0x7ffff800, 1 << 32, ^uint64(0)} {
		b := riscv64.NewByteAssembler(abi.StackNoUse, 0)
		if err := b.Literal(machine.Instruction{Op: machine.OpLiteralLoad, Bits: 64, A: machine.VectorRegister(3), Disp: disp}); !errors.Is(err, compile.ErrBackendLimit) {
			t.Fatalf("riscv literal %#x: %v", disp, err)
		}
	}
	for _, arch := range []string{"ppc64", "ppc64le"} {
		p, err := abi.NewPPC64Profile(arch, abi.C, abi.PPC64ELFv2)
		if err != nil {
			t.Fatal(err)
		}
		e, err := ppc64.NewEmitter(p, abi.StackNoUse)
		if err != nil {
			t.Fatal(err)
		}
		for _, disp := range []uint64{0x7fff8008, 1 << 32, ^uint64(0)} {
			if _, err := e.Expand(machine.Instruction{Op: machine.OpLiteralLoad, Bits: 64, A: machine.VectorRegister(3), Disp: disp}, 0); !errors.Is(err, compile.ErrBackendLimit) {
				t.Fatalf("%s literal %#x: %v", arch, disp, err)
			}
		}
	}
}
