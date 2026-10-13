package compile_test

import (
	"os"
	"runtime"
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestGiantCompileScratchDecaysToMedium(t *testing.T) {
	if os.Getenv("NYANJIT_PERF_RETAINED") != "1" {
		t.Skip("set NYANJIT_PERF_RETAINED=1 to run retained scratch workloads")
	}
	if !compile.HostBackend() {
		t.Skip("native compiler target required")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 24}}
	for _, size := range []int{700, 1000} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			// Keep one per-P scratch alive even when -race randomly discards Pool.Put.
			// This tests decay itself, independent of sync.Pool's eviction policy.
			compile.PinCompileScratch(t)
			medium := workload.Scopes(size)
			steady := func() uint64 {
				for round := 0; round < 4; round++ {
					for i := 0; i < 50; i++ {
						if _, err := lowerUnit(medium, compile.TargetSpec{}, f); err != nil {
							t.Fatal(err)
						}
					}
					runtime.GC()
				}
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				return m.HeapAlloc
			}
			base := steady()
			giant := workload.Scopes(10000)
			if _, err := lowerUnit(giant, compile.TargetSpec{}, f); err != nil {
				t.Fatal(err)
			}
			giant = nil
			after := steady()
			delta := int64(after) - int64(base)
			t.Logf("Scopes(%d) after giant: base=%d after=%d excess=%d B", size, base, after, delta)
			if delta > 2<<20 {
				t.Fatalf("medium workload retains %d excess bytes; want <=2 MiB", delta)
			}
			runtime.KeepAlive(medium)
		})
	}
}

func TestGiantTinyScratchAlternation(t *testing.T) {
	if os.Getenv("NYANJIT_PERF_RETAINED") != "1" {
		t.Skip("set NYANJIT_PERF_RETAINED=1 to run retained scratch workloads")
	}
	if !compile.HostBackend() {
		t.Skip("native compiler target required")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	compile.CheckGiantTinyScratchAlternation(t, workload.Scopes(10000), workload.Tiny(0))
}

// The complete cycle includes each tiny compile, so allocations cannot hide in
// setup. Running this against an overlay on the baseline gives identical inputs.
func BenchmarkPrepareAlternating(b *testing.B) {
	if !compile.HostBackend() {
		b.Skip("native compiler target required")
	}
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 24}}
	tiny := workload.Tiny(0)
	for _, workload := range []struct {
		name    string
		program *ir.SealedProgram
	}{
		{"scopes10000", workload.Scopes(10000)},
		{"chain8000", workload.Chain(8000)},
		{"scopes800", workload.Scopes(800)},
	} {
		for _, gap := range []int{1, 2, 3, 10} {
			b.Run(workload.name+"/1:"+strconv.Itoa(gap), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := lowerTemplate(workload.program, compile.TargetSpec{}, f); err != nil {
						b.Fatal(err)
					}
					for i := 0; i < gap; i++ {
						if _, err := lowerTemplate(tiny, compile.TargetSpec{}, f); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}
