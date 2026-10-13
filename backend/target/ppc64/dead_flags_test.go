package ppc64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestDeadPPCFlagsKeepCarryAcrossCRWriter(t *testing.T) {
	s := machine.NewStream(machine.StreamOptions{Size: func(machine.Instruction) int { return 4 }, FlagFacts: func(i machine.Instruction) machine.FlagState {
		if i.Op == machine.OpBinary && !i.PreserveFlags {
			return machine.FlagState{Modified: machine.FlagSign | machine.FlagZero | machine.FlagCarry}
		}
		if i.Op == machine.OpCompareImmediate {
			return machine.FlagState{Modified: machine.FlagSign | machine.FlagZero}
		}
		return machine.FlagState{}
	}, FlagEffect: func(i machine.Instruction) bool {
		return i.Op == machine.OpBinary && !i.PreserveFlags || i.Op == machine.OpCompareImmediate
	}})
	s.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 64})
	s.Append(machine.Instruction{Op: machine.OpCompareImmediate})
	s.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: machine.BranchCarry, Imm: 0})
	s.Append(machine.Instruction{Op: machine.OpReturn})
	if err := elideDeadFlags(s, []int{12}, []bool{true}); err != nil {
		t.Fatal(err)
	}
	if s.Instructions()[0].PreserveFlags {
		t.Fatal("CR comparison does not kill live XER carry")
	}
	s.Replace([]machine.Instruction{{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 64}, {Op: machine.OpReturn}})
	if err := elideDeadFlags(s, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !s.Instructions()[0].PreserveFlags {
		t.Fatal("dead arithmetic flags were retained")
	}
}
