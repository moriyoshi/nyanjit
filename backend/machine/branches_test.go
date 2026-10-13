package machine

import "testing"

func TestCompactBranchesKeepsZeroSizedCompareAtLabel(t *testing.T) {
	stream := NewStream(StreamOptions{
		Size: func(ins Instruction) int {
			if ins.Op == OpCompareImmediate {
				return 0
			}
			return 4
		},
		FlagEffect: func(Instruction) bool { return false },
	})
	stream.Append(Instruction{Op: OpJump, Imm: 0})
	stream.Append(Instruction{Op: OpCompareImmediate, A: 11})
	stream.Append(Instruction{Op: OpJumpIf, Imm: 1})
	stream.Append(Instruction{Op: OpReturn})
	labels, err := CompactBranches(stream, []int{4, 8}, []bool{true, true})
	if err != nil {
		t.Fatal(err)
	}
	insns := stream.Instructions()
	if len(insns) != 3 || insns[0].Op != OpCompareImmediate || insns[1].Op != OpJumpIf || insns[2].Op != OpReturn || labels[0] != 0 {
		t.Fatalf("compacted stream = %+v, labels = %v", insns, labels)
	}
}

func TestCompactBranchesInvertsConditionalOverJump(t *testing.T) {
	stream := NewStream(StreamOptions{
		Size:       func(Instruction) int { return 4 },
		FlagEffect: func(Instruction) bool { return false },
	})
	stream.Append(Instruction{Op: OpBinary})
	stream.Append(Instruction{Op: OpJumpIf, Condition: 0, Imm: 1})
	stream.Append(Instruction{Op: OpJump, Imm: 0})
	stream.Append(Instruction{Op: OpReturn})
	labels, err := CompactBranches(stream, []int{0, 12}, []bool{true, true})
	if err != nil {
		t.Fatal(err)
	}
	insns := stream.Instructions()
	if len(insns) != 3 || insns[1].Op != OpJumpIf || insns[1].Condition != 1 || insns[1].Imm != 0 || labels[1] != 8 {
		t.Fatalf("direct loop backedge=%+v labels=%v", insns, labels)
	}
}

func TestCompactBranchesKeepsReferencedSelfJump(t *testing.T) {
	stream := NewStream(StreamOptions{
		Size:       func(Instruction) int { return 4 },
		FlagEffect: func(Instruction) bool { return false },
	})
	stream.Append(Instruction{Op: OpJumpIf, Condition: 0, Imm: 1})
	stream.Append(Instruction{Op: OpJump, Imm: 0})
	stream.Append(Instruction{Op: OpReturn})
	_, err := CompactBranches(stream, []int{4, 8}, []bool{true, true})
	if err != nil {
		t.Fatal(err)
	}
	insns := stream.Instructions()
	if len(insns) != 3 || insns[1].Op != OpJump || insns[1].Imm != 0 {
		t.Fatalf("independent infinite loop was changed: %+v", insns)
	}
}
