package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/ir"
)

func newPPC64SelectionPolicy(t *testing.T, architecture string, flavor abi.Flavor) ppc64.Policy {
	t.Helper()
	profile, err := abi.NewPPC64Profile(architecture, flavor)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := ppc64.NewPolicy(profile, abi.StackNoUse)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestPolicySelectsMemoryWithoutChangingLogicalOrder(t *testing.T) {
	for _, architecture := range []string{"ppc64le"} {
		policy := newPPC64SelectionPolicy(t, architecture, abi.C)
		for _, order := range []op.ByteOrder{op.ByteOrderNative, op.ByteOrderLittleEndian, op.ByteOrderBigEndian} {
			ops := []ir.Instruction{
				{Op: op.OpParam, Bits: 64}, {Op: op.OpConst, Bits: 64, Imm: 8},
				{Op: op.OpAdd, Bits: 64, A: 0, B: 1},
				{Op: op.OpLoad64, A: 2, ByteOrder: order},
				{Op: op.OpAdd, Bits: 64, A: 0, B: 1},
				{Op: op.OpStore64, A: 4, B: 3, ByteOrder: order},
			}
			selected, err := testSelectProgram(ops, nil, 1, 3, nil, 8, policy)
			if err != nil {
				t.Fatalf("%s order %d: %v", architecture, order, err)
			}
			loads, stores := 0, 0
			for _, ins := range selected.Instructions {
				if ins.Op == machine.SelectedLoadOffset || ins.Op == machine.SelectedStoreOffset {
					if ins.ByteOrder != order || ins.Disp != 8 {
						t.Fatalf("fold dropped memory contract: %+v", ins)
					}
					if ins.Op == machine.SelectedLoadOffset {
						loads++
					} else {
						stores++
					}
				}
			}
			if loads != 1 || stores != 1 {
				t.Fatalf("fold counts = load %d, store %d", loads, stores)
			}
		}
		pair, ok := policy.Integer128Result()
		if !ok || pair != [2]backend.Register{3, 4} {
			t.Fatalf("%s logical pair = %v", architecture, pair)
		}
	}
}
