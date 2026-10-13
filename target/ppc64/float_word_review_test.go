package ppc64_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/ppc64"
)

func TestPOWER8Float32WordExtraction(t *testing.T) {
	for _, arch := range []string{"ppc64", "ppc64le"} {
		profile, err := abi.NewPPC64Profile(arch, abi.C, abi.PPC64ELFv2)
		if err != nil {
			t.Fatal(err)
		}
		for _, cpu := range []abi.PPC64CPU{abi.PPC64Power8, abi.PPC64Power9, abi.PPC64Power10} {
			e, err := ppc64.NewEmitterForCPU(profile, abi.StackNoUse, false, cpu)
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range []machine.Instruction{
				{Op: machine.OpFloatMove, Bits: 32, A: 3, B: machine.VectorRegister(4)},
				{Op: machine.OpFloatBinary, Bits: 32, Arithmetic: ir.OpFAdd, A: 3, B: 4, C: 5},
			} {
				seq, err := e.Expand(i, 0)
				if err != nil {
					t.Fatal(err)
				}
				text := ppc64.NewTextAssembler()
				for n, x := range seq {
					if err := text.Visit(x); err != nil {
						t.Fatal(err)
					}
					if x.Op == ppc64.Mfvsrwz && cpu == abi.PPC64Power8 {
						if n < 2 || seq[n-2].Op != ppc64.Xscvdpspn || seq[n-1].Op != ppc64.Xxsldwi || seq[n-1].Shift != 3 {
							t.Fatalf("POWER8 reads undefined word: %+v", seq)
						}
						word, err := ppc64.Encode(seq[n-1])
						if err != nil || word != 0xf0000310 {
							t.Fatalf("rotation encoding %#x,%v", word, err)
						}
					}
				}
				if got := strings.Contains(text.Text(), "xxsldwi vs0, vs0, vs0, 3"); got != (cpu == abi.PPC64Power8) {
					t.Fatalf("%s CPU%s rotate=%t: %s", arch, cpu, got, text.Text())
				}
			}
		}
	}
}
