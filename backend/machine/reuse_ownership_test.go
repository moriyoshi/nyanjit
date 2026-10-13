package machine

import (
	"testing"
)

func TestExpiredBorrowsStayPoisonedAfterReuse(t *testing.T) {
	var w Workspace
	opts := StreamOptions{Size: func(Instruction) int { return 4 }}
	w.Stream(opts) // Rewind preserves this prefix.
	s := w.Stream(opts)
	s.Append(Instruction{Op: OpReturn})
	old := s.Instructions()
	w.Rewind(1)
	w.Stream(opts).Append(Instruction{Op: OpMove})
	if workspacePoison && old[0].Op != 255 {
		t.Fatal("re-lease overwrote expired Instructions poison")
	}
}

func TestSelectedInputsOwnTheirStorage(t *testing.T) {
	for _, op := range []SelectedOpcode{SelectedHomeBoundary, SelectedVector, SelectedWordCarry, SelectedCallC, SelectedCallGo, SelectedSyscall, SelectedBinary, SelectedSelect} {
		ins := SelectedInstruction{Op: op, A: 1, B: 2, C: 3, Args: []int{1, 2, 3}}
		first, second := ins.Inputs(), ins.Inputs()
		first[0] = 99
		if ins.Args[0] != 1 || ins.A != 1 || second[0] != 1 {
			t.Fatalf("opcode %d Inputs aliases source or sibling", op)
		}
	}
}

func TestWorkspaceBudgetDropsOnlyLargestNeeded(t *testing.T) {
	var w Workspace
	s := w.Stream(StreamOptions{})
	s.instructions = make([]Instruction, 0, 1000)
	s.sizes = make([]int, 0, 100)
	s.layout.pending = make([]int, 0, 100)
	before, _ := w.RetainedBytes(0)
	largest := workBytes(s.instructions)
	freed, next := w.TrimToBudget(before - 1)
	if freed != largest || next != workBytes(s.sizes) || s.instructions != nil || cap(s.sizes) != 100 || cap(s.layout.pending) != 100 {
		t.Fatalf("freed=%d next=%d; smallest buffers lost", freed, next)
	}
	after, _ := w.RetainedBytes(0)
	if before-after != freed {
		t.Fatal("budget and retained accounting disagree")
	}
}
