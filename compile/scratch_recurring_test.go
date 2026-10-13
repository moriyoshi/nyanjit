package compile

import (
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

// recurringScratch returns a scratch whose puts go to a private pool, so the
// test keeps using it without racing other tests for the shared pool.
func recurringScratch() *compileScratch {
	return &compileScratch{machine: new(machine.Workspace), pool: new(sync.Pool)}
}

// leaseLarge starts a lowering cycle with one stream and one selection lease
// demanding n elements each.
func leaseLarge(s *compileScratch, n int) *machine.Stream {
	s.machine.Reset()
	s.selection.reset()
	stream := s.machine.Stream(machine.StreamOptions{Size: func(machine.Instruction) int { return 1 }})
	stream.Reserve(n)
	b := s.selection.lease()
	b.instructions = appendBufferTracked(&b.instructions, &b.history.instructions, n)
	return stream
}

func largeBuffers(s *compileScratch) (machineLarge, selectionLarge bool) {
	_, machineLarge = s.machine.RetainedBytes(scratchPointerLimit)
	_, selectionLarge = s.selection.retainedBytes(scratchPointerLimit)
	return
}

func TestScratchRecurringLargeLoweringKeepsStreams(t *testing.T) {
	s := recurringScratch()
	var one [1]machine.Instruction
	large := scratchPointerLimit/bufferBytes(one[:]) + 1
	leaseLarge(s, large)
	putCompileScratch(s)
	if m, sel := largeBuffers(s); m || sel {
		t.Fatal("a one-off large lowering pinned its pointer-bearing buffers")
	}
	leaseLarge(s, large)
	putCompileScratch(s)
	if m, sel := largeBuffers(s); !m || !sel {
		t.Fatalf("a recurring large lowering lost its buffers (machine %v, selection %v)", m, sel)
	}
	if stream := leaseLarge(s, large); cap(stream.Instructions()) < large {
		t.Fatal("the next large lowering regrew its stream")
	}
	putCompileScratch(s)
	if s.retained > scratchTotalLimit {
		t.Fatalf("scratch retains %d B, budget %d", s.retained, scratchTotalLimit)
	}

	// Sustained small lowerings still release the large buffers, at the pace
	// of the fourfold decay band.
	small := 0
	for ; small < 64; small++ {
		if m, sel := largeBuffers(s); !m && !sel {
			break
		}
		leaseLarge(s, 16)
		putCompileScratch(s)
	}
	if small == 64 || small < 8 {
		t.Fatalf("large buffers released after %d small lowerings; want the decay band", small)
	}
	t.Logf("released after %d small lowerings", small)
	// Demand that no longer recurs returns to the ordinary pointer limit.
	for i := 0; i < scratchRecurrence; i++ {
		leaseLarge(s, 16)
		putCompileScratch(s)
	}
	leaseLarge(s, large)
	putCompileScratch(s)
	if m, sel := largeBuffers(s); m || sel {
		t.Fatal("a large lowering after the recurrence window pinned its buffers")
	}
}

func TestScratchRecurringStreamsStayWithinTotalBudget(t *testing.T) {
	s := recurringScratch()
	var one [1]machine.Instruction
	giant := scratchTotalLimit/bufferBytes(one[:]) + 1
	for i := 0; i < 3; i++ {
		leaseLarge(s, giant)
		putCompileScratch(s)
		if s.retained > scratchTotalLimit {
			t.Fatalf("cycle %d: scratch retains %d B, budget %d", i, s.retained, scratchTotalLimit)
		}
	}
}

func TestScratchRecurringStreamsPoisonExpiredBorrows(t *testing.T) {
	if !scratchPoison {
		t.Skip("poison build required")
	}
	s := recurringScratch()
	var one [1]machine.Instruction
	large := scratchPointerLimit/bufferBytes(one[:]) + 1
	leaseLarge(s, large)
	putCompileScratch(s)
	stream := leaseLarge(s, large)
	stream.Append(machine.Instruction{Op: machine.OpReturn})
	old := stream.Instructions()
	selected := s.selection.selections[0].instructions[:1]
	selected[0] = machineInstruction{Op: machine.SelectedBinary, Dst: 1}
	putCompileScratch(s)
	if m, sel := largeBuffers(s); !m || !sel {
		t.Fatal("recurring buffers were not kept")
	}
	if old[0].Op != 255 || selected[0].Op != 255 {
		t.Fatal("kept buffers did not poison expired borrows")
	}
	stream = leaseLarge(s, large)
	stream.Append(machine.Instruction{Op: machine.OpMove})
	if old[0].Op != 255 || stream.Instructions()[0].Op != machine.OpMove {
		t.Fatal("the next lease reused the expired array")
	}
	putCompileScratch(s)
}
