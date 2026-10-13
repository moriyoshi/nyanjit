package compile_test

import (
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
)

// One large compile followed by a steady trickle of tiny compiles: the large
// scratch is reused by every tiny compile and therefore never ages out.
func TestGiantCompileScratchDecays(t *testing.T) {
	if !compile.HostBackend() {
		t.Skip("native compiler target required")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 24}}
	tiny := workload.Tiny(0)
	steady := func() uint64 {
		for round := 0; round < 10; round++ {
			for i := 0; i < 200; i++ {
				if _, err := lowerUnit(tiny, compile.TargetSpec{}, f); err != nil {
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
	big := workload.Scopes(10000)
	if _, err := lowerUnit(big, compile.TargetSpec{}, f); err != nil {
		t.Fatal(err)
	}
	big = nil
	after := steady()
	if delta := int64(after) - int64(base); delta > 4<<20 {
		t.Errorf("scratch retained %d B after tiny compiles; want <=4 MiB", delta)
	}
	t.Logf("heap after steady tiny compiles: before giant %d B, after giant %d B, delta %d B", base, after, int64(after)-int64(base))
}
