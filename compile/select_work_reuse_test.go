package compile

import (
	"testing"
)

func TestSelectionPoisonReleasePreservesReuseAndHistory(t *testing.T) {
	if !scratchPoison {
		t.Skip("poison build required")
	}
	b := selectionBuffers{}
	b.instructions = make([]machineInstruction, 3, 32)
	b.refs = make([]int, 8, 64)
	b.history.refs.Observe(8)
	old, refs, history := b.instructions, b.refs, b.history
	b.release(true)
	if cap(b.instructions) != cap(old) || cap(b.refs) != cap(refs) || b.history != history {
		t.Fatal("release discarded capacity or history")
	}
	if &old[0] == &b.instructions[:cap(b.instructions)][0] || &refs[0] == &b.refs[0] {
		t.Fatal("expired arrays were reused")
	}
	if old[0].Op != 255 || b.instructions[:cap(b.instructions)][0].Op != 255 || refs[0] != -0x5a5a || b.refs[0] != -0x5a5a {
		t.Fatal("old or fresh array was not poisoned")
	}
	resetBuffer(&b.refs, 8)
	if b.refs[0] != 0 || refs[0] != -0x5a5a {
		t.Fatal("reset did not clear only the fresh array")
	}
}
