package ppc64_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

func TestFloatSpillLargeOffsets(t *testing.T) {
	for _, arch := range []string{"ppc64le"} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			e := emitterFor(t, arch, stack)
			for _, op := range []machine.Opcode{machine.OpFloatSpill, machine.OpFloatReload} {
				i := machine.Instruction{Op: op, A: backend.VectorRegister(3), Slot: 4095}
				seq, err := e.Expand(i, 0)
				if err != nil {
					t.Fatal(err)
				}
				if len(seq)*4 != e.Size(i) {
					t.Fatal("spill size differs from expansion")
				}
				last := seq[len(seq)-1]
				if stack == abi.StackPlatform {
					if last.B != 11 || last.Imm != 0 {
						t.Fatalf("large offset was not materialized: %+v", seq)
					}
					found := false
					for _, reg := range e.ImplicitWrites(i) {
						found = found || reg == 11
					}
					if !found {
						t.Fatal("large spill omitted R11 clobber")
					}
				} else if last.B != 29 || last.Imm != 32760 {
					t.Fatalf("small offset expanded incorrectly: %+v", seq)
				}
				for _, native := range seq {
					if _, err := ppc64.Encode(native); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}
