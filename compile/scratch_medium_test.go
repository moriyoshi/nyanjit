package compile_test

import (
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
)

func TestMediumCompileScratchReturnsToBaseline(t *testing.T) {
	if os.Getenv("NYANJIT_PERF_RETAINED") != "1" {
		t.Skip("set NYANJIT_PERF_RETAINED=1 for retained scratch workloads")
	}
	if !compile.HostBackend() {
		t.Skip("native target required")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	for _, prior := range []int{700, 1000} {
		t.Run(fmt.Sprint(prior), func(t *testing.T) {
			scratch := compile.PinCompileScratch(t)
			p := workload.Scopes(300)
			f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 24}}
			steady := func() (int, uint64) {
				// 200 cycles cover the 32-cycle steady-state switch and more than
				// ten 16-cycle decay periods. Extra cycles only repeat convergence;
				// keep this bounded enough to run under QEMU on 386 too.
				for i := 0; i < 200; i++ {
					if _, err := lowerTemplate(p, compile.TargetSpec{}, f); err != nil {
						t.Fatal(err)
					}
				}
				runtime.GC()
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				return scratch.Retained(), m.HeapAlloc
			}
			base, baseHeap := steady()
			big := workload.Scopes(prior)
			if _, err := lowerTemplate(big, compile.TargetSpec{}, f); err != nil {
				t.Fatal(err)
			}
			big = nil
			after, afterHeap := steady()
			t.Logf("Scopes(%d)->Scopes(300): retained %d -> %d (excess %d B), heap %d -> %d (excess %d B)", prior, base, after, after-base, baseHeap, afterHeap, int64(afterHeap)-int64(baseHeap))
			// Allow allocator capacity rounding up to 1/64 of the steady
			// baseline. In particular, 32-bit maps and value rows round to
			// different capacities than their 64-bit counterparts. Keep a
			// 4 KiB floor for small baselines, while rejecting retained growth.
			if after > base+max(4096, base/64) {
				t.Fatalf("permanent medium scratch slack: %d B", after-base)
			}
			runtime.KeepAlive(p)
		})
	}
}
