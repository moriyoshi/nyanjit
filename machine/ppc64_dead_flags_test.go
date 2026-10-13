package machine

import (
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func TestDeadPPCFlagsKeepCarryAcrossCRWriter(t *testing.T) {
	s := NewStream(StreamOptions{Size: func(Instruction) int { return 4 }, FlagFacts: func(i Instruction) FlagState {
		if i.Op == OpBinary && !i.PreserveFlags {
			return FlagState{Modified: FlagSign | FlagZero | FlagCarry}
		}
		if i.Op == OpCompareImmediate {
			return FlagState{Modified: FlagSign | FlagZero}
		}
		return FlagState{}
	}, FlagEffect: func(i Instruction) bool { return i.Op == OpBinary && !i.PreserveFlags || i.Op == OpCompareImmediate }})
	s.Append(Instruction{Op: OpBinary, Arithmetic: ir.OpAdd, Bits: 64})
	s.Append(Instruction{Op: OpCompareImmediate})
	s.Append(Instruction{Op: OpJumpIf, Condition: BranchCarry, Imm: 0})
	s.Append(Instruction{Op: OpReturn})
	if err := ElidePPC64DeadFlags(s, []int{12}, []bool{true}); err != nil {
		t.Fatal(err)
	}
	if s.Instructions()[0].PreserveFlags {
		t.Fatal("CR comparison does not kill live XER carry")
	}
	s.Replace([]Instruction{{Op: OpBinary, Arithmetic: ir.OpAdd, Bits: 64}, {Op: OpReturn}})
	if err := ElidePPC64DeadFlags(s, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !s.Instructions()[0].PreserveFlags {
		t.Fatal("dead arithmetic flags were retained")
	}
}
