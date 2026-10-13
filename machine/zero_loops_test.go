package machine

import (
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func TestARMZeroLoopPreservesLiveFlags(t *testing.T) {
	for _, liveCarry := range []bool{false, true} {
		s := NewStream(StreamOptions{Size: func(i Instruction) int {
			if i.Op == OpBranchZero && i.Long {
				return 8
			}
			return 4
		}, FlagEffect: func(i Instruction) bool { return i.Op == OpDec && !i.PreserveFlags }, FlagFacts: func(i Instruction) FlagState {
			if i.Op == OpDec && !i.PreserveFlags {
				return FlagState{Modified: FlagSign | FlagZero | FlagCarry}
			}
			return FlagState{}
		}})
		s.Append(Instruction{Op: OpMove, A: 2, B: 3})
		s.Append(Instruction{Op: OpDec, A: 2, B: 2})
		s.Append(Instruction{Op: OpJumpIf, Condition: ir.BranchNotEqual, Imm: 0})
		if liveCarry {
			s.Append(Instruction{Op: OpWordCarry, CarryMode: CarryFlags})
		}
		s.Append(Instruction{Op: OpReturn})
		_, err := FuseZeroLoops(s, []int{0}, []bool{true}, "arm64")
		if err != nil {
			t.Fatal(err)
		}
		fused := s.Instructions()[1].PreserveFlags
		if fused == liveCarry {
			t.Fatalf("carry live=%v fused=%v", liveCarry, fused)
		}
		if fused && s.Instructions()[2].Op != OpBranchZero {
			t.Fatal("missing direct branch")
		}
	}
}
func TestRISCZeroLoopEliminatesComparePseudo(t *testing.T) {
	s := NewStream(StreamOptions{Size: func(i Instruction) int {
		if i.Op == OpCompareImmediate {
			return 0
		}
		return 4
	}, FlagEffect: func(Instruction) bool { return false }})
	s.Append(Instruction{Op: OpDec, A: 10, B: 10})
	s.Append(Instruction{Op: OpCompareImmediate, A: 10})
	s.Append(Instruction{Op: OpJumpIf, Condition: ir.BranchNotEqual, Imm: 0})
	s.Append(Instruction{Op: OpReturn})
	labels, err := FuseZeroLoops(s, []int{0}, []bool{true}, "riscv64")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Instructions()) != 3 || s.Instructions()[1].Op != OpBranchZero || labels[0] != 0 || s.PC() != 12 {
		t.Fatalf("stream=%v labels=%v PC=%d", s.Instructions(), labels, s.PC())
	}
}
