package machine_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
)

func TestRelaxBranchesKeepsValidatedSizes(t *testing.T) {
	calls := 0
	s := machine.NewStream(machine.StreamOptions{
		Size:       func(i machine.Instruction) int { calls++; return arm64.PhysicalSize(i, abi.StackNoUse, 0) },
		FlagEffect: func(machine.Instruction) bool { return false },
	})
	s.Append(machine.Instruction{Op: machine.OpJumpIf, Long: true})
	s.Append(machine.Instruction{Op: machine.OpMove, A: 1, B: 2})
	s.Append(machine.Instruction{Op: machine.OpReturn})
	calls = 0
	labels, err := machine.RelaxBranches(s, []int{12}, []bool{true}, func(machine.Instruction, int, int, int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("size queries = %d, want only the changed branch", calls)
	}
	if s.PC() != 12 || s.SizeAt(0) != 4 || labels[0] != 8 {
		t.Fatalf("bad relaxed layout: PC=%d branch=%d labels=%v", s.PC(), s.SizeAt(0), labels)
	}
}

func TestCompactionKeepsUnchangedSizes(t *testing.T) {
	calls := 0
	s := machine.NewStream(machine.StreamOptions{
		Size:       func(i machine.Instruction) int { calls++; return arm64.PhysicalSize(i, abi.StackNoUse, 0) },
		FlagEffect: func(machine.Instruction) bool { return false },
	})
	s.Append(machine.Instruction{Op: machine.OpMove, A: 1, B: 2})
	s.Append(machine.Instruction{Op: machine.OpJump})
	s.Append(machine.Instruction{Op: machine.OpReturn})
	calls = 0
	labels, err := machine.CompactBranches(s, []int{8}, []bool{true})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("unchanged instructions were measured %d more times", calls)
	}
	if s.PC() != 8 || labels[0] != 4 {
		t.Fatalf("bad compacted layout: PC=%d labels=%v", s.PC(), labels)
	}
}

func TestCompactionRemeasuresChangedBranchFields(t *testing.T) {
	for _, invert := range []bool{false, true} {
		changedQueries := 0
		s := machine.NewStream(machine.StreamOptions{
			Size: func(i machine.Instruction) int {
				n := arm64.PhysicalSize(i, abi.StackNoUse, 0)
				if i.Op == machine.OpJumpIf && ((invert && i.Condition == 1) || (!invert && i.Imm == 1)) {
					changedQueries++
					return n + 4
				}
				return n
			},
			FlagEffect: func(machine.Instruction) bool { return false },
		})
		s.Append(machine.Instruction{Op: machine.OpJumpIf})
		var labels []int
		if invert {
			// Both edges name the same label: inversion changes Condition only,
			// so the Imm-based cache invalidation cannot hide a missing check.
			s.Append(machine.Instruction{Op: machine.OpJump})
			s.Append(machine.Instruction{Op: machine.OpReturn})
			s.Append(machine.Instruction{Op: machine.OpReturn})
			labels = []int{8, 12}
		} else {
			s.Append(machine.Instruction{Op: machine.OpMove, A: 1, B: 2})
			s.Append(machine.Instruction{Op: machine.OpReturn})
			s.Append(machine.Instruction{Op: machine.OpJump, Imm: 1})
			s.Append(machine.Instruction{Op: machine.OpReturn})
			labels = []int{12, 16}
		}
		if _, err := machine.CompactBranches(s, labels, []bool{true, true}); err != nil {
			t.Fatal(err)
		}
		if changedQueries != 1 || s.SizeAt(0) != 8 {
			t.Fatalf("invert=%v: changed queries=%d branch size=%d", invert, changedQueries, s.SizeAt(0))
		}
		wantLabel := uint64(1)
		if invert {
			wantLabel = 0
		}
		if i := s.Instructions()[0]; i.Imm != wantLabel || (invert && i.Condition != 1) {
			t.Fatalf("bad rewritten branch %+v", i)
		}
	}
}

func TestPublicRewriteStillChecksEveryInstruction(t *testing.T) {
	calls := 0
	s := machine.NewStream(machine.StreamOptions{
		Size:       func(i machine.Instruction) int { calls++; return arm64.PhysicalSize(i, abi.StackNoUse, 0) },
		FlagEffect: func(machine.Instruction) bool { return false },
	})
	s.Append(machine.Instruction{Op: machine.OpMove, A: 1, B: 2})
	calls = 0
	s.ReplaceKeepingFacts([]machine.Instruction{{Op: machine.OpMove, A: 255, B: 1}})
	if calls != 1 || s.PC() != 0 {
		t.Fatalf("unvalidated public rewrite: calls=%d PC=%d", calls, s.PC())
	}
}
