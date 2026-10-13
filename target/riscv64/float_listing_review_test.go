package riscv64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func TestSignInjectionListingReview(t *testing.T) {
	for _, tc := range []struct {
		word uint32
		want string
	}{
		{0x22422253, "fabs.d f4, f4"}, {0x225292d3, "fneg.d f5, f5"},
		{0x22420253, "fmv.d f4, f4"}, {0x20422253, "fabs.s f4, f4"},
		{0x205292d3, "fneg.s f5, f5"}, {0x20420253, "fmv.s f4, f4"},
		{0x22628253, "fsgnj.d f4, f5, f6"}, {0x22629253, "fsgnjn.d f4, f5, f6"}, {0x2262a253, "fsgnjx.d f4, f5, f6"},
	} {
		if got := decodeWord(tc.word, 0); got != tc.want {
			t.Fatalf("%#x decoded %q, want %q", tc.word, got, tc.want)
		}
	}
}

func TestFloatBitsListingMatchesPlan(t *testing.T) {
	for _, w := range []uint8{32, 64} {
		for _, op := range []ir.Opcode{ir.OpAnd, ir.OpXor} {
			i := machine.Instruction{Op: machine.OpFloatBits, Bits: w, A: machine.VectorRegister(4), B: machine.VectorRegister(4), Arithmetic: op}
			words := floatWords(i)
			if len(words) != 1 {
				t.Fatal("sign operation grew beyond one instruction")
			}
			name, width := "fabs", "s"
			if op == ir.OpXor {
				name = "fneg"
			}
			if w == 64 {
				width = "d"
			}
			want := name + "." + width + " f4, f4"
			if got := decodeWord(words[0], 0); got != want {
				t.Fatalf("byte/text sign plan differs: %q want %q", got, want)
			}
		}
	}
}
