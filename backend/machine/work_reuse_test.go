package machine

import "testing"

func TestSelectedEmptyInputsOwnCapacity(t *testing.T) {
	args := make([]int, 0, 4)
	args[:cap(args)][0] = 42
	ins := SelectedInstruction{Op: SelectedCallC, Args: args}
	inputs := append(ins.Inputs(), 7)
	if inputs[0] != 7 || args[:cap(args)][0] != 42 {
		t.Fatal("appending to empty Inputs overwrote instruction storage")
	}
}

func TestPoisonReleasePreservesReuseAndHistory(t *testing.T) {
	if !workspacePoison {
		t.Skip("poison build required")
	}
	history := BufferHistory{recent: 512 | 4<<58, need: 8}
	s := Stream{instructions: make([]Instruction, 4, 32), sizes: make([]int, 4, 32)}
	s.layout.positions = make([]int, 3, 64)
	s.layout.history.positions = history
	positions := s.layout.positions
	s.releaseWork(true)
	if cap(s.instructions) != 32 || cap(s.sizes) != 32 || cap(s.layout.positions) != 64 || s.layout.history.positions != history {
		t.Fatal("stream release lost capacities or history")
	}
	if &positions[0] == &s.layout.positions[0] || positions[0] != -0x5a5a || s.layout.positions[0] != -0x5a5a {
		t.Fatal("stream storage was not detached and poisoned")
	}
}
