package ppc64_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/ppc64"
)

func newPolicy(t *testing.T, architecture string, flavor abi.Flavor) ppc64.Policy {
	t.Helper()
	convention := abi.PPC64CABIUnspecified
	if flavor == abi.C {
		convention = abi.PPC64ELFv2
	}
	profile, err := abi.NewPPC64Profile(architecture, flavor, convention)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := ppc64.NewPolicy(profile, abi.StackNoUse)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestRegisterPolicyReservesRuntimeAndExpansionState(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		policy := newPolicy(t, "ppc64", flavor)
		seen := make(map[machine.Register]bool)
		for _, register := range policy.Registers() {
			if seen[register] {
				t.Fatalf("register %d appears twice", register)
			}
			seen[register] = true
			for _, reserved := range []machine.Register{0, 1, 2, 11, 12, 13, 29, 30, 31} {
				if register == reserved {
					t.Fatalf("flavor %d allocates reserved R%d", flavor, register)
				}
			}
		}
		if !seen[policy.ScratchRegister()] || seen[policy.WorkspaceRegister()] || seen[policy.CallTargetRegister()] {
			t.Fatal("scratch, workspace, and call-target ownership overlap")
		}
		address := policy.ReturnAddress()
		if !address.InRegister() || address.Resource.Class != machine.ResourceLinkRegister {
			t.Fatalf("return address = %+v", address)
		}
		if _, general := address.GeneralRegister(); general {
			t.Fatal("PPC64 LR was classified as a GPR")
		}
		if policy.ResourceName(address.Resource) != "LR" {
			t.Fatal("return address name is not LR")
		}
		if policy.ManagedFrameLimit() != 0 {
			t.Fatal("unimplemented native metadata must not advertise managed calls")
		}
	}
}

func TestBigEndianPolicySelectsMemoryWithoutChangingLogicalOrder(t *testing.T) {
	for _, architecture := range []string{"ppc64", "ppc64le"} {
		policy := newPolicy(t, architecture, abi.C)
		for _, order := range []ir.ByteOrder{ir.ByteOrderNative, ir.ByteOrderLittleEndian, ir.ByteOrderBigEndian} {
			ops := []ir.Instruction{
				{Op: ir.OpParam, Bits: 64}, {Op: ir.OpConst, Bits: 64, Imm: 8},
				{Op: ir.OpAdd, Bits: 64, A: 0, B: 1},
				{Op: ir.OpLoad64, A: 2, ByteOrder: order},
				{Op: ir.OpAdd, Bits: 64, A: 0, B: 1},
				{Op: ir.OpStore64, A: 4, B: 3, ByteOrder: order},
			}
			selected, err := machine.SelectProgram(ops, nil, 1, 3, nil, 8, policy)
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
		if !ok || architecture == "ppc64" && pair != [2]machine.Register{4, 3} || architecture == "ppc64le" && pair != [2]machine.Register{3, 4} {
			t.Fatalf("%s logical pair = %v", architecture, pair)
		}
	}
}

func TestBranchRangesAndDisplacementAlignment(t *testing.T) {
	policy := newPolicy(t, "ppc64", abi.C)
	for _, test := range []struct {
		op       machine.Opcode
		long     bool
		distance int
		fits     bool
	}{
		{machine.OpJumpIf, false, -32768, true}, {machine.OpJumpIf, false, 32764, true},
		{machine.OpJumpIf, false, 32768, false}, {machine.OpJumpIf, false, 2, false},
		{machine.OpJump, false, -(1 << 25), true}, {machine.OpJump, false, (1 << 25) - 4, true},
		{machine.OpJump, false, 1 << 25, false},
		{machine.OpJumpIf, true, 1 << 25, true}, {machine.OpJumpIf, true, (1 << 25) + 4, false},
	} {
		if got := policy.BranchFits(machine.Instruction{Op: test.op, Long: test.long}, 0, test.distance, 0); got != test.fits {
			t.Errorf("%+v fits = %v", test, got)
		}
	}
	for _, test := range []struct {
		displacement int64
		fits         bool
	}{{-32768, true}, {32764, true}, {32768, false}, {3, false}} {
		if got := policy.CanFoldBaseOffset(uint64(test.displacement)); got != test.fits {
			t.Errorf("displacement %d fits = %v", test.displacement, got)
		}
	}
}
