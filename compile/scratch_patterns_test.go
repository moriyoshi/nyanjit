package compile

import (
	"os"
	"strconv"
	"sync"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestScratchBufferDemandDecays(t *testing.T) {
	// Every big request must survive gaps of one, two, three or ten tiny requests.
	// Then steady medium demand must evict only its oversized buffer.
	for _, gap := range []int{1, 2, 3, 10} {
		var history machine.BufferHistory
		var mapHistory machine.BufferHistory
		large := make([]int, 20000)
		small := make([]int, 16)
		for cycle := 0; cycle < 100; cycle++ {
			history.Observe(20000)
			mapHistory.Observe(1)
			mapHistory.FinishCycle(1)
			decayBuffer(&large, &history)
			for i := 0; i < gap; i++ {
				history.Observe(4)
				if mapHistory.FinishCycle(1) {
					t.Fatalf("1:%d alternation forgot single-entry map", gap)
				}
				decayBuffer(&large, &history)
			}
			if large == nil {
				t.Fatalf("1:%d alternation discarded reusable buffer", gap)
			}
		}
		for i := 0; i < 100; i++ {
			history.Observe(1000)
			decayBuffer(&large, &history)
		}
		if large != nil || small == nil {
			t.Fatal("medium demand did not trim buffers independently")
		}
	}
}

// checkGiantTinyScratchAlternation alternates one giant lowering with tiny
// ones and checks that the pinned scratch keeps its scope rows. The workloads
// come from TestGiantTinyScratchAlternation in package compile_test.
func checkGiantTinyScratchAlternation(t *testing.T, big, tiny *ir.SealedProgram) {
	t.Helper()
	f := Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 24}}
	for _, gap := range []int{1, 2, 3, 10} {
		t.Run(strconv.Itoa(gap), func(t *testing.T) {
			scratch := pinCompileScratch(t)
			for cycle := 0; cycle < 4; cycle++ {
				if _, err := lowerUnit(big, TargetSpec{}, f); err != nil {
					t.Fatal(err)
				}
				rows := scratch.homes.rows
				if len(rows) == 0 {
					t.Fatal("giant fixture did not allocate scope rows")
				}
				for i := 0; i < gap; i++ {
					if _, err := lowerUnit(tiny, TargetSpec{}, f); err != nil {
						t.Fatal(err)
					}
				}
				if cap(scratch.homes.rows) != cap(rows) || &scratch.homes.rows[0] != &rows[0] {
					t.Fatalf("tiny gap discarded scope rows: before len/cap=%d/%d after=%d/%d history=%+v retained=%d", len(rows), cap(rows), len(scratch.homes.rows), cap(scratch.homes.rows), scratch.homes.history.rows, scratch.retained)
				}
				if !scratchPoison && scratch.homes.rows[0].homes == nil {
					t.Fatalf("1:%d cycle %d dropped reusable root map", gap, cycle)
				}
			}
		})
	}
}

// Pin a single scratch so retention tests exercise decay even when sync.Pool
// drops entries (-race intentionally drops a random fraction of puts).
func pinCompileScratch(t *testing.T) *compileScratch {
	t.Helper()
	scratch := new(compileScratch)
	pinned := &sync.Pool{New: func() any { return scratch }}
	host := mustTarget(t, TargetSpec{})
	oldGlobal, oldHost := compileScratchPool, host.scratch
	compileScratchPool, host.scratch = pinned, pinned
	t.Cleanup(func() { compileScratchPool, host.scratch = oldGlobal, oldHost })
	return scratch
}

func TestScratchTotalBudgetTrimsIndividualRows(t *testing.T) {
	if os.Getenv("NYANJIT_PERF_RETAINED") != "1" {
		t.Skip("set NYANJIT_PERF_RETAINED=1 to check the aggregate map budget")
	}
	scratch := &compileScratch{machine: new(machine.Workspace)}
	scratch.allocation.definedAt = make([]int, 16)
	saved := &scratch.allocation.definedAt[0]
	scratch.homes.used = true
	rowBytes := 64*intSize() + retainedMapBytes[int, machine.Home](64) + retainedMapBytes[int, bool](64)
	scratch.homes.rows = make([]homeRow, (scratchTotalLimit+(2<<20))/rowBytes+1)
	for i := range scratch.homes.rows {
		r := &scratch.homes.rows[i]
		r.homes = make(map[int]machine.Home, 64)
		r.modified = make(map[int]bool, 64)
		r.ids = make([]int, 64)
		r.history.Observe(64)
		r.history.FinishCycle(64)
		for id := 0; id < 64; id++ {
			r.homes[id] = machine.Home{}
			r.modified[id] = true
			r.ids[id] = id
		}
	}
	before, _ := scratch.homes.retained()
	if before <= scratchTotalLimit {
		t.Fatal("fixture does not exceed the total budget")
	}
	putCompileScratch(scratch)
	if scratch.retained > scratchTotalLimit {
		t.Fatalf("scratch retains %d B, budget %d", scratch.retained, scratchTotalLimit)
	}
	if scratch.machine == nil || &scratch.allocation.definedAt[0] != saved {
		t.Fatal("total budget discarded unrelated storage")
	}
	dropped := 0
	for _, r := range scratch.homes.rows {
		if r.homes == nil {
			dropped++
		}
	}
	if !scratchPoison && (dropped == 0 || dropped == len(scratch.homes.rows)) {
		t.Fatalf("want partial row trim, dropped %d/%d", dropped, len(scratch.homes.rows))
	}
}

func intSize() int { return int(unsafe.Sizeof(int(0))) }
