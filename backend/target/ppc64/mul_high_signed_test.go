package ppc64_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

func TestSignedHighMultiplyEncodingsAndExpansion(t *testing.T) {
	// GNU assembler vectors for mulhd r3,r4,r5 and mulhw r3,r4,r5.
	for _, test := range []struct {
		op   ppc64.Opcode
		word uint32
	}{{ppc64.Mulhd, 0x7c642892}, {ppc64.Mulhw, 0x7c642896}} {
		word, err := ppc64.Encode(ppc64.Instruction{Op: test.op, A: 3, B: 4, C: 5})
		if err != nil || word != test.word {
			t.Fatalf("opcode %d got %#x,%v; want %#x", test.op, word, err, test.word)
		}
	}
	e := emitterFor(t, "ppc64le", abi.StackNoUse)
	for _, test := range []struct {
		width uint8
		ops   []ppc64.Opcode
	}{
		{64, []ppc64.Opcode{ppc64.Mulhd}},
		{32, []ppc64.Opcode{ppc64.Mulhw, ppc64.Rldicl}},
		{16, []ppc64.Opcode{ppc64.Extsh, ppc64.Extsh, ppc64.Mulld, ppc64.Rldicl}},
		{8, []ppc64.Opcode{ppc64.Extsb, ppc64.Extsb, ppc64.Mulld, ppc64.Rldicl}},
	} {
		ins := machine.Instruction{Op: machine.OpMulHighSigned, Bits: test.width, A: 4, B: 4, C: 5}
		seq, err := e.Expand(ins, 0)
		if err != nil {
			t.Fatal(err)
		}
		if e.FlagFacts(ins).Modified != 0 || len(e.SpecialWrites(ins)) != 0 {
			t.Fatalf("%d-bit signed high multiplication modified flags", test.width)
		}
		if len(seq) != len(test.ops) {
			t.Fatalf("%d bits: %+v", test.width, seq)
		}
		for i, op := range test.ops {
			if seq[i].Op != op {
				t.Fatalf("%d bits: %+v", test.width, seq)
			}
		}
		if last := seq[len(seq)-1]; test.width < 64 && (last.A != 4 || last.B != 4 || last.Mask != 64-test.width || test.width < 32 && last.Shift != 64-test.width) {
			t.Fatalf("%d bits: result not extracted: %+v", test.width, last)
		}
	}
	if _, err := e.Expand(machine.Instruction{Op: machine.OpMulHighSigned, Bits: 12, A: 4, B: 4, C: 5}, 0); err == nil {
		t.Fatal("12-bit signed high product accepted")
	}
}
