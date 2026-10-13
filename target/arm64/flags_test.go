package arm64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func TestSubImmediateNarrowCarryFacts(t *testing.T) {
	for _, width := range []uint8{8, 16} {
		mask := uint64(1)<<width - 1
		for k := uint64(1); k <= mask; k++ {
			imm := uint64(int64(k<<(64-width)) >> (64 - width))
			encoded, _, _, ok := addSubImmediate(ir.OpSub, width, imm)
			if !ok {
				continue
			}
			facts := FlagFacts(machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: ir.OpSub, Bits: width, Imm: imm})
			if want := encoded == ir.OpSub; facts.Carry.Valid != want {
				t.Fatalf("width%d immediate%#x encoded%v carry%+v; valid should be %t", width, k, encoded, facts.Carry, want)
			}
			if facts.Modified&machine.FlagCarry == 0 {
				t.Fatal("discarded fact must still invalidate old C")
			}
		}
	}
	for _, width := range []uint8{32, 64} {
		facts := FlagFacts(machine.Instruction{Op: machine.OpBinaryImmediate, Arithmetic: ir.OpSub, Bits: width, Imm: ^uint64(0)})
		if !facts.Carry.Valid || !facts.Carry.NoBorrow {
			t.Fatalf("width%d lost valid full-width carry: %+v", width, facts)
		}
	}
}
