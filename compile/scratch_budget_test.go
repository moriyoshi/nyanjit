package compile

import (
	"os"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestScratchTotalBudgetKeepsSmallerBuffers(t *testing.T) {
	if os.Getenv("NYANJIT_PERF_RETAINED") != "1" {
		t.Skip("set NYANJIT_PERF_RETAINED=1 to check aggregate scratch limits")
	}
	s := pinCompileScratch(t)
	s.machine = new(machine.Workspace)
	smaller := []*[]int{
		&s.allocation.homeValues, &s.allocation.definedAt, &s.allocation.previousCall,
		&s.allocation.entryWords, &s.allocation.liveFP, &s.homes.ending, &s.homes.scopes,
		&s.homes.banks, &s.homes.bankData, &s.homes.cursors, &s.homes.scopeStack,
		&s.homes.outer, &s.control.positions, &s.control.incoming, &s.homes.demand, &s.homes.fpDemand,
	}
	for _, v := range smaller {
		*v = make([]int, (2<<20)/intSize())
	}
	s.control.results = make([]int, (3<<20)/intSize())
	putCompileScratch(s)
	if s.control.results != nil {
		t.Fatal("largest buffer survived over-budget trim")
	}
	for i, v := range smaller {
		if bufferBytes(*v) != 2<<20 {
			t.Fatalf("smaller buffer %d was discarded", i)
		}
	}
	if s.retained != scratchTotalLimit {
		t.Fatalf("retained %d B, want exact budget %d", s.retained, scratchTotalLimit)
	}
}
