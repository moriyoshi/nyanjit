package machine

import (
	"github.com/moriyoshi/nyanjit/backend"
	"testing"
)

func TestLayoutBorrowBoundary(t *testing.T) {
	for _, pooled := range []bool{false, true} {
		var w Workspace
		opts := StreamOptions{Size: func(Instruction) int { return 4 }}
		s := NewStream(opts)
		if pooled {
			s = w.Stream(opts)
		}
		s.Append(Instruction{Op: OpReturn})
		first, err := ResolveBranches(s, nil, nil, func(Instruction, int, int, int) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		saved := append([]LocatedInstruction(nil), first...)
		_, err = ResolveBranches(s, nil, nil, func(Instruction, int, int, int) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		if workspacePoison && first[0].Instruction.Op != 255 {
			t.Fatal("second resolve rewrote expired result with plausible data")
		}

		_, err = CompactBranches(s, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if workspacePoison && first[0].Instruction.Op != 255 {
			t.Fatal("layout boundary failed to poison expired result")
		}
		if saved[0].Instruction.Op != OpReturn {
			t.Fatal("owned copy changed")
		}
		next, err := ResolveBranches(s, nil, nil, func(Instruction, int, int, int) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		if next[0].Instruction.Op != OpReturn {
			t.Fatal("layout lost current instructions")
		}
		snapshot := s.Instructions()
		_, err = CompactBranches(s, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if workspacePoison && snapshot[0].Op != 255 {
			t.Fatal("next layout did not poison expired Instructions")
		}

		if pooled {
			w.Reset()
			if workspacePoison && next[0].Instruction.Op != 255 {
				t.Fatal("Reset failed to poison expired result")
			}
		}
	}
}

func TestWorkspaceTrimAccounting(t *testing.T) {
	var w Workspace
	s := w.Stream(StreamOptions{})
	s.Reserve(100)
	s.layout.pending = make([]int, 1000)
	s.layout.threadState = make([]uint8, 1000)
	w.Trim(512)
	if bytes, large := w.RetainedBytes(512); large {
		t.Fatalf("Trim left oversized retained buffers (%d bytes)", bytes)
	}
}

func TestRegisterSetMembership(t *testing.T) {
	s := backend.NewRegisterSet(3, 1, 3)
	if !s.Contains(3) || s.Index(3) != 0 || s.Contains(2) || s.Index(2) != -1 {
		t.Fatal("wrong membership")
	}
	if (backend.RegisterSet{}).Contains(0) {
		t.Fatal("zero set contains a register")
	}
}

func TestTrimPoisonsDiscardedBorrows(t *testing.T) {
	var workspace Workspace
	stream := workspace.Stream(StreamOptions{Size: func(Instruction) int { return 4 }})
	for i := 0; i < 100; i++ {
		stream.Append(Instruction{Op: OpMove})
	}
	stream.Append(Instruction{Op: OpReturn})
	located, err := ResolveBranches(stream, nil, nil, func(Instruction, int, int, int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	instructions := stream.Instructions()
	workspace.Trim(512)
	if workspacePoison && (located[0].Instruction.Op != 255 || instructions[0].Op != 255) {
		t.Fatal("Trim left discarded public slices readable as valid instructions")
	}
}
