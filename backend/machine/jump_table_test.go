package machine

import (
	"slices"
	"testing"
)

// Branch layout threads jump-only labels through a jump table's entries,
// keeps every block a table reaches, drops the code it cannot fall into,
// and resolves the entries to PCs without changing the stream's labels.
func TestJumpTableBranchLayout(t *testing.T) {
	stream := NewStream(StreamOptions{
		Size: func(ins Instruction) int {
			if ins.Op == OpJumpTable {
				return 8 + 4*len(ins.Table)
			}
			return 4
		},
		FlagEffect: func(Instruction) bool { return false },
	})
	// 0: table over labels 0 (a jump on to label 2), 1 and 1 again; 20: never
	// reached; 24: label 0; 28: label 1; 32: label 2.
	stream.Append(Instruction{Op: OpJumpTable, A: 1, B: 2, Table: []int{0, 1, 1}})
	stream.Append(Instruction{Op: OpBinary})
	stream.Append(Instruction{Op: OpJump, Imm: 2})
	stream.Append(Instruction{Op: OpReturn})
	stream.Append(Instruction{Op: OpReturn})
	labels, err := CompactBranches(stream, []int{24, 28, 32}, []bool{true, true, true})
	if err != nil {
		t.Fatal(err)
	}
	insns := stream.Instructions()
	if len(insns) != 3 || insns[0].Op != OpJumpTable || !slices.Equal(insns[0].Table, []int{2, 1, 1}) {
		t.Fatalf("compacted stream = %+v, labels = %v", insns, labels)
	}
	located, err := ResolveBranches(stream, labels, []bool{true, true, true}, func(Instruction, int, int, int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{labels[2], labels[1], labels[1]}; !slices.Equal(located[0].Instruction.Table, want) || !slices.Equal(stream.Instructions()[0].Table, []int{2, 1, 1}) {
		t.Fatalf("resolved table %v, want %v; stream keeps %v", located[0].Instruction.Table, want, stream.Instructions()[0].Table)
	}
	stream.Replace([]Instruction{{Op: OpJumpTable, Table: []int{0, 5}}, {Op: OpReturn}})
	if _, err := ResolveBranches(stream, []int{0}, []bool{true}, func(Instruction, int, int, int) bool { return true }); err != ErrInvalidBranch {
		t.Fatalf("unbound table label: %v", err)
	}
}
