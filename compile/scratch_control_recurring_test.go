package compile

import (
	"testing"
	"unsafe"
)

// leaseControlArenas runs one lowering's control pass demanding n fragment
// and body elements, keeping the arenas as the control emitter does.
func leaseControlArenas(s *compileScratch, n int) {
	s.machine.Reset()
	s.selection.reset()
	a := &s.control.arenas
	a.fragments = keepArena(a.fragments[:0], &a.history.fragments, n)
	a.bodies = keepArena(a.bodies[:0], &a.history.bodies, n)
}

func largeControlArenas(s *compileScratch) (fragments, bodies bool) {
	a := &s.control.arenas
	return bufferBytes(a.fragments) > scratchBufferLimit, bufferBytes(a.bodies) > scratchBufferLimit
}

// controlArenaDemand returns an element count that puts both arenas over
// scratchBufferLimit.
func controlArenaDemand(s *compileScratch) int {
	a := &s.control.arenas
	return scratchBufferLimit/int(min(unsafe.Sizeof(a.fragments[0]), unsafe.Sizeof(a.bodies[0]))) + 1
}

func TestScratchRecurringLargeLoweringKeepsControlArenas(t *testing.T) {
	s := recurringScratch()
	large := controlArenaDemand(s)
	leaseControlArenas(s, large)
	putCompileScratch(s)
	if f, b := largeControlArenas(s); f || b {
		t.Fatal("a one-off large lowering pinned its control arenas")
	}
	leaseControlArenas(s, large)
	putCompileScratch(s)
	if f, b := largeControlArenas(s); !f || !b {
		t.Fatalf("a recurring large lowering lost its control arenas (fragments %v, bodies %v)", f, b)
	}
	kept := &s.control.arenas.fragments[:1][0]
	leaseControlArenas(s, large)
	if &s.control.arenas.fragments[:1][0] != kept || cap(s.control.arenas.bodies) < large {
		t.Fatal("the next large lowering regrew its control arenas")
	}
	putCompileScratch(s)
	if s.retained > scratchTotalLimit {
		t.Fatalf("scratch retains %d B, budget %d", s.retained, scratchTotalLimit)
	}

	// Sustained small lowerings still release the large arenas, at the pace
	// of the fourfold decay band.
	small := 0
	for ; small < 64; small++ {
		if f, b := largeControlArenas(s); !f && !b {
			break
		}
		leaseControlArenas(s, 16)
		putCompileScratch(s)
	}
	if small == 64 || small < 8 {
		t.Fatalf("large control arenas released after %d small lowerings; want the decay band", small)
	}
	t.Logf("released after %d small lowerings", small)
	// Demand that no longer recurs returns to the ordinary buffer limit.
	for i := 0; i < scratchRecurrence; i++ {
		leaseControlArenas(s, 16)
		putCompileScratch(s)
	}
	leaseControlArenas(s, large)
	putCompileScratch(s)
	if f, b := largeControlArenas(s); f || b {
		t.Fatal("a large lowering after the recurrence window pinned its control arenas")
	}
}

func TestScratchRecurringControlArenasStayWithinTotalBudget(t *testing.T) {
	s := recurringScratch()
	a := &s.control.arenas
	giant := scratchTotalLimit/int(unsafe.Sizeof(a.fragments[0])) + 1
	for i := 0; i < 3; i++ {
		leaseControlArenas(s, giant)
		putCompileScratch(s)
		if s.retained > scratchTotalLimit {
			t.Fatalf("cycle %d: scratch retains %d B, budget %d", i, s.retained, scratchTotalLimit)
		}
	}
}

func TestScratchRecurringControlArenasPoisonExpiredBorrows(t *testing.T) {
	if !scratchPoison {
		t.Skip("poison build required")
	}
	s := recurringScratch()
	large := controlArenaDemand(s)
	leaseControlArenas(s, large)
	putCompileScratch(s)
	leaseControlArenas(s, large)
	old := carve(&s.control.arenas.fragments, 1)[:1]
	old[0] = physicalInsn{Op: 1}
	putCompileScratch(s)
	if f, b := largeControlArenas(s); !f || !b {
		t.Fatal("recurring control arenas were not kept")
	}
	if old[0].Op != 255 {
		t.Fatal("kept control arenas did not poison expired borrows")
	}
}
