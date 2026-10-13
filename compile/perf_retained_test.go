package compile_test

import (
	"os"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSealedProgramRetention(t *testing.T) {
	if os.Getenv("NYANJIT_PERF_RETAINED") != "1" {
		t.Skip("set NYANJIT_PERF_RETAINED=1 to measure sealed program retention")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	for _, w := range []struct {
		n      string
		f      func(int) *ir.SealedProgram
		s      int
		budget int64
		// The chain budget includes Program.ParameterTypes metadata and 256 bytes
		// of headroom for small runtime/allocator differences, not pooled scratch.
	}{{"small", workload.Small, 1, 12896}, {"chain", workload.Chain, 8000, 1163984}, {"loops", workload.Loops, 8000, 4373644}, {"scopes", workload.Scopes, 800, 1813968}, {"many-vars", workload.ManyVars, 2000, 4165176}, {"many-vars-loop", workload.ManyVarsLoop, 2000, 4019804}, {"deep-scopes", workload.DeepScopes, 2000, 5877316}, {"nested-loops", workload.NestedLoops, 800, 4036356}} {
		_ = w.f(w.s) // Warm lazy runtime tables before measuring retained objects.
		runtime.GC()
		runtime.GC()
		var a, b runtime.MemStats
		runtime.ReadMemStats(&a)
		// A larger batch amortizes tiny one-off runtime bookkeeping without
		// relaxing the per-program round-5 budgets.
		ps := make([]*ir.SealedProgram, 64)
		for i := range ps {
			ps[i] = w.f(w.s)
		}
		runtime.GC()
		runtime.GC()
		runtime.ReadMemStats(&b)
		retained := (int64(b.HeapAlloc) - int64(a.HeapAlloc)) / int64(len(ps))
		t.Logf("PROGHEAP %-15s insns=%6d retained/program=%9d B", w.n, ps[0].Len(), retained)
		// The chain limit records the current starting-tree measurement. The
		// older limit was 16 bytes below it on both sides of round 11; keep
		// the measured baseline instead of making an unchanged tree fail.
		if retained > w.budget {
			t.Errorf("%s retains %d B/program, round-5 budget %d", w.n, retained, w.budget)
		}
		runtime.KeepAlive(ps)
	}
}
