package ppc64_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/ppc64"
)

func TestUnsignedHighMultiplyEncodingsAndEffects(t *testing.T) {
	// Independent GNU assembler POWER8 vectors.
	for _, test := range []struct {
		op   ppc64.Opcode
		word uint32
	}{{ppc64.Mulhdu, 0x7c642812}, {ppc64.Mulhwu, 0x7c642816}} {
		word, err := ppc64.Encode(ppc64.Instruction{Op: test.op, A: 3, B: 4, C: 5})
		if err != nil || word != test.word {
			t.Fatalf("opcode%d got %#x,%v; want %#x", test.op, word, err, test.word)
		}
	}
	e := emitterFor(t, "ppc64", abi.StackNoUse)
	for _, width := range []uint8{8, 16, 32, 64} {
		ins := machine.Instruction{Op: machine.OpMulHigh, Bits: width, A: 4, B: 4, C: 5}
		seq, err := e.Expand(ins, 0)
		if err != nil {
			t.Fatal(err)
		}
		if e.FlagFacts(ins).Modified != 0 || len(e.SpecialWrites(ins)) != 0 {
			t.Fatal("unsigned high multiplication modified flags")
		}
		if (width == 64 && seq[0].Op != ppc64.Mulhdu) || (width == 32 && seq[0].Op != ppc64.Mulhwu) {
			t.Fatal("missing native high product")
		}
		if width >= 32 && len(seq) != 1 {
			t.Fatal("native high product unexpectedly expanded")
		}
	}
}
