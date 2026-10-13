package compile

import (
	"errors"
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestNarrowMemorySelectionMaterializesAddress(t *testing.T) {
	profile, e := abi.NewPPC64Profile("ppc64le", abi.C)
	if e != nil {
		t.Fatal(e)
	}
	policy, e := ppc64.NewPolicyForCPU(profile, abi.StackNoUse, abi.PPC64Power8)
	if e != nil {
		t.Fatal(e)
	}
	for _, bits := range []uint8{16, 32} {
		for _, disp := range []uint64{0, 2047, 2048, 32767, 32768, ^uint64(0) - 3} {
			t.Run(fmt.Sprintf("%d/%x", bits, disp), func(t *testing.T) {
				load, store := op.OpLoad16, op.OpStore16
				sl, ss := machine.SelectedLoad16, machine.SelectedStore16
				if bits == 32 {
					load, store = op.OpLoad32, op.OpStore32
					sl, ss = machine.SelectedLoad32, machine.SelectedStore32
				}
				ops := []ir.Instruction{{Op: op.OpParam, Bits: 64}, {Op: op.OpParam, Bits: 64}, {Op: op.OpConst, Bits: 64, Imm: disp}, {Op: op.OpAddScaled, Bits: 64, A: 0, B: 1, Imm: 4}, {Op: op.OpAdd, Bits: 64, A: 3, B: 2}, {Op: store, Bits: bits, A: 4, B: 1, Aligned: true}, {Op: load, Bits: bits, A: 4, Aligned: true}}
				p, e := testSelectProgram(ops, nil, 2, 6, nil, 8, policy)
				if e != nil {
					t.Fatal(e)
				}
				found := 0
				for _, i := range p.Instructions {
					if i.Op == ss || i.Op == sl {
						found++
						if i.A != 4 || i.Bits != bits {
							t.Fatalf("width/address lost: %+v", i)
						}
					}
					if i.Op == machine.SelectedLoadAddress || i.Op == machine.SelectedStoreAddress || i.Op == machine.SelectedLoadOffset || i.Op == machine.SelectedStoreOffset {
						t.Fatalf("used 64-bit memory form: %+v", i)
					}
				}
				if found != 2 || p.Values[6].Bits != bits || p.Values[6].Words != 1 {
					t.Fatalf("incorrect scalar metadata: %+v", p)
				}
				ops[5].Aligned = false
				if _, e := testSelectProgram(ops, nil, 2, 6, nil, 8, policy); !errors.Is(e, backend.ErrBackendLimit) {
					t.Fatalf("unsafe unlegalized access: %v", e)
				}
			})
		}
	}
}
