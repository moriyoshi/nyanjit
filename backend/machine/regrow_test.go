package machine

import "testing"

// A stream whose buffer a trim discarded still remembers its demand, so the
// next lowering grows straight toward it instead of regrowing step by step.
func TestTrimmedStreamGrowsToRememberedDemand(t *testing.T) {
	const n = 5000
	var w Workspace
	opts := StreamOptions{Size: func(Instruction) int { return 1 }}
	fill := func() (*Stream, int) {
		w.Reset()
		s := w.Stream(opts)
		s.Reserve(8)
		grows := 0
		for range n {
			if len(s.instructions) == cap(s.instructions) {
				grows++
			}
			s.Append(Instruction{Op: OpMove})
		}
		return s, grows
	}
	_, first := fill()
	w.TrimPointers(0)
	w.Decay()
	s, grows := fill()
	if cap(s.instructions) != n {
		t.Fatalf("regrown capacity %d, want the remembered %d", cap(s.instructions), n)
	}
	// Doubling from 8 reaches 4096 in nine steps; the tenth stops at n.
	if grows > 10 || grows >= first {
		t.Fatalf("regrowth took %d steps (first lowering %d)", grows, first)
	}
	w.Trim(0)
	w.Decay()
	if s, _ := fill(); cap(s.instructions) != n {
		t.Fatalf("after Trim, regrown capacity %d, want %d", cap(s.instructions), n)
	}
}
