package machine_test

import (
	"reflect"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
)

func TestSecurityRepeatedLayoutPasses(t *testing.T) {
	var w machine.Workspace
	s := w.Stream(machine.StreamOptions{Size: func(ins machine.Instruction) int { return arm64.PhysicalSize(ins, abi.StackNoUse, 0) }})
	s.Append(machine.Instruction{Op: machine.OpConst, A: 1, Imm: 5})
	s.Append(machine.Instruction{Op: machine.OpJumpIf, Long: true, Imm: 0})
	s.Append(machine.Instruction{Op: machine.OpConst, A: 0, Imm: 7})
	s.Append(machine.Instruction{Op: machine.OpReturn})
	labels, bound := []int{s.PC() - 4}, []bool{true}
	policy := arm64.NewPolicy(abi.C, abi.StackNoUse)
	labels, e := machine.RelaxBranches(s, labels, bound, policy.BranchFits)
	if e != nil {
		t.Fatal(e)
	}
	want := append([]machine.Instruction(nil), s.Instructions()...)
	for i := 0; i < 4; i++ {
		labels, e = machine.RelaxBranches(s, labels, bound, policy.BranchFits)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(want, s.Instructions()) {
			t.Fatalf("pass %d destroyed instructions", i)
		}
	}
	for i := 0; i < 3; i++ {
		labels, e = machine.CompactBranches(s, labels, bound)
		if e != nil {
			t.Fatal(e)
		}
		labels, e = machine.RelaxBranches(s, labels, bound, policy.BranchFits)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(want, s.Instructions()) {
			t.Fatal("mixed layout passes changed executable stream")
		}
	}
	// Failures at both early validation and after scratch acquisition must leave
	// every scratch buffer distinct from the live stream, including next time.
	for _, sequence := range []string{"CRCRXR", "CRYRXCR", "CRXRYRCR"} {
		for _, step := range sequence {
			switch step {
			case 'C':
				labels, e = machine.CompactBranches(s, labels, bound)
			case 'R':
				labels, e = machine.RelaxBranches(s, labels, bound, policy.BranchFits)
			case 'X':
				_, err := machine.RelaxBranches(s, []int{12345}, bound, policy.BranchFits)
				if err == nil {
					t.Fatal("invalid Relax succeeded")
				}
			case 'Y':
				_, err := machine.CompactBranches(s, []int{12345}, bound)
				if err == nil {
					t.Fatal("invalid Compact succeeded")
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(want, s.Instructions()) {
				t.Fatalf("sequence %s after %c changed executable stream", sequence, step)
			}
		}
	}

}
