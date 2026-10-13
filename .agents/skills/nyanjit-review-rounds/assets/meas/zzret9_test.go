package nyanjit_test

import (
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

// ZZ9_PRIOR (0 = none), ZZ9_STEADY: steady medium workload at the current GOMAXPROCS (P goroutines),
// then one prior compile per goroutine, then steady again; report heap excess vs base per round.
func TestZZ9Steady(t *testing.T) {
	prior, _ := strconv.Atoi(os.Getenv("ZZ9_PRIOR"))
	size, _ := strconv.Atoi(os.Getenv("ZZ9_STEADY"))
	if size == 0 {
		t.Skip("set ZZ9_STEADY")
	}
	P := runtime.GOMAXPROCS(0)
	steadyP := workload.Scopes(size)
	if size < 0 {
		steadyP = workload.Tiny(0)
	}
	par := func(n int, p *ir.SealedProgram) {
		var wg sync.WaitGroup
		for g := 0; g < P; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < n; i++ {
					zzPrep(t, p)
				}
			}()
		}
		wg.Wait()
	}
	round := func() uint64 {
		par(50, steadyP)
		runtime.GC()
		return zzHeap().HeapAlloc
	}
	med := func(v []int64) int64 {
		w := append([]int64(nil), v...)
		sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
		return w[len(w)/2]
	}
	var bs []int64
	for r := 0; r < 10; r++ {
		h := int64(round())
		if r >= 2 {
			bs = append(bs, h)
		}
	}
	base := uint64(med(bs))
	line := "STEADY9 P=" + strconv.Itoa(P) + " prior=" + strconv.Itoa(prior) + " steady=" + strconv.Itoa(size) + " base=" + strconv.FormatUint(base, 10)
	if prior > 0 {
		pp := workload.Scopes(prior)
		par(1, pp)
		pp = nil
	}
	var ex []int64
	for r := 1; r <= 16; r++ {
		ex = append(ex, int64(round())-int64(base))
	}
	line += " r1=" + strconv.FormatInt(ex[0], 10) + " med1_4=" + strconv.FormatInt(med(ex[:4]), 10) + " med9_16=" + strconv.FormatInt(med(ex[8:]), 10) + " r16=" + strconv.FormatInt(ex[15], 10)
	runtime.GC()
	runtime.GC()
	runtime.GC()
	line += " idle=" + strconv.FormatInt(int64(zzHeap().HeapAlloc)-int64(base), 10)
	t.Log(line)
	runtime.KeepAlive(steadyP)
}

// zzPrep and zzHeap were defined by the retired zzalt_test.go probe; they are
// kept here so this file builds on its own. zzPrep is the old compile.Prepare
// (or, with ZZMODE=lower, the old compile.Lower without encoding).
var zzLowerOnly = os.Getenv("ZZMODE") == "lower"

func zzPrep(t *testing.T, p *ir.SealedProgram) {
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err == nil {
		var u *compile.Unit
		u, err = compile.Lower(target, p, compile.Func{ABI: abi.C, Context: compile.ContextNative, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 24}})
		if err == nil && !zzLowerOnly {
			_, err = u.Template()
		}
	}
	if err != nil {
		t.Fatal(err)
	}
}

func zzHeap() runtime.MemStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m
}
