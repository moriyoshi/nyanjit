package ppc64_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

func TestWordCarryEncodingAndResourceEffects(t *testing.T) {
	// Independent GNU POWER8 assembler vectors.
	for _, test := range []struct {
		ins  ppc64.Instruction
		word uint32
	}{
		{ppc64.Instruction{Op: ppc64.Adde, A: 3, B: 4, C: 5}, 0x7c642914},
		{ppc64.Instruction{Op: ppc64.Subfe, A: 3, B: 4, C: 5}, 0x7c652110},
		{ppc64.Instruction{Op: ppc64.Addze, A: 3, B: 11}, 0x7c6b0194},
		{ppc64.Instruction{Op: ppc64.Subfze, A: 3, B: 11}, 0x7c6b0190},
		{ppc64.Instruction{Op: ppc64.Subfic, A: 11, B: 5}, 0x21650000},
	} {
		word, err := ppc64.Encode(test.ins)
		if err != nil || word != test.word {
			t.Fatalf("%+v got%#x,%v;want%#x", test.ins, word, err, test.word)
		}
	}
	e := emitterFor(t, "ppc64le", abi.StackNoUse)
	for _, operation := range []op.Opcode{op.OpAdd, op.OpSub} {
		for _, mode := range []machine.CarryMode{machine.CarryValue, machine.CarryBitValue, machine.CarryOperands, machine.CarryFlags} {
			ins := machine.Instruction{Op: machine.OpWordCarry, Arithmetic: operation, Bits: 64, A: 3, B: 4, C: 5, D: 6, E: 7, CarryMode: mode}
			seq, err := e.Expand(ins, 0)
			if err != nil {
				t.Fatal(err)
			}
			if mode == machine.CarryFlags && len(seq) != 1 {
				t.Fatal("proven carry unexpectedly materialized")
			}
			facts := e.FlagFacts(ins)
			if facts.Modified != machine.FlagSign|machine.FlagZero|machine.FlagCarry || !facts.Zero.Valid || !facts.Sign.Valid || facts.Carry.Valid {
				t.Fatalf("three-operand carry invented binary facts: %+v", facts)
			}
			ins.Op = machine.OpCarryBit
			facts = e.FlagFacts(ins)
			if facts.Modified != machine.FlagCarry {
				t.Fatalf("carry extraction modified CR0: %+v", facts)
			}
		}
	}
}
