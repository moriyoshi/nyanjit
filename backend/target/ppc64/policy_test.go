package ppc64_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

func newPolicy(t *testing.T, architecture string, flavor abi.Flavor) ppc64.Policy {
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

func TestRegisterPolicyReservesRuntimeAndExpansionState(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		policy := newPolicy(t, "ppc64le", flavor)
		seen := make(map[backend.Register]bool)
		for register := range policy.Registers().All() {
			if seen[register] {
				t.Fatalf("register %d appears twice", register)
			}
			seen[register] = true
			for _, reserved := range []backend.Register{0, 1, 2, 11, 12, 13, 29, 30, 31} {
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

func TestBranchRangesAndDisplacementAlignment(t *testing.T) {
	policy := newPolicy(t, "ppc64le", abi.C)
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
