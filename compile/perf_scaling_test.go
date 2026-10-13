//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"math"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/ir"
)

// TestPrepareScaling excludes IR construction and executable mapping. Four
// geometrically spaced sizes span 8x; five interleaved runs each fit a log-log
// slope. Median per-doubling growth must meet the unchanged 2.3x limit.
func TestPrepareScaling(t *testing.T) { testCompilerScaling(t, false) }

// TestBuilderScaling includes construction, validation, sealing and analyses.
func TestBuilderScaling(t *testing.T) { testCompilerScaling(t, true) }

// scalingGrowth fits log2(time) against log2(size) at four doubling-spaced
// points. Using the full slope reduces dependence on one noisy endpoint.
func scalingGrowth(times [4]float64) float64 {
	slope := 0.0
	for n, elapsed := range times {
		slope += (float64(n) - 1.5) * math.Log2(elapsed)
	}
	return math.Exp2(slope / 5)
}

func testCompilerScaling(t *testing.T, building bool) {
	t.Helper()
	if os.Getenv("NYANJIT_PERF_SCALING") != "1" {
		t.Skip("set NYANJIT_PERF_SCALING=1 to run the native timing gate")
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, w := range performanceWorkloads() {
		// Scope-heavy sizes span 200..1600: smaller depths hide the constant of
		// an ancestor walk behind linear lowering work, even after the walk is
		// quadratic.
		n := w.ScalingSize
		if n == 0 {
			continue
		}
		t.Run(w.Name, func(t *testing.T) {
			sizes := [4]int{n / 2, n, 2 * n, 4 * n}
			programs := [4]*ir.SealedProgram{}
			for index, size := range sizes {
				programs[index] = w.Build(size)
			}
			measure := func(index int) float64 {
				// Interleaving includes hundreds of smaller compiles between sizes.
				// Decay may correctly release the next size's old scratch; warm that
				// size once so the slope measures steady compilation, not one resize
				// amortized over a different number of iterations at each size.
				if !building {
					if err := preparePerformance(programs[index], w); err != nil {
						t.Fatal(err)
					}
				}
				start := time.Now()
				runs := 0
				for runs == 0 || time.Since(start) < 160*time.Millisecond {
					if building {
						built := w.Build(sizes[index])
						runtime.KeepAlive(built)
					} else if err := preparePerformance(programs[index], w); err != nil {
						t.Fatal(err)
					}
					runs++
				}
				return float64(time.Since(start).Nanoseconds()) / float64(runs)
			}
			for index := range sizes {
				measure(index)
			}
			growth := make([]float64, 5)
			var samples [4][]float64
			for run := range growth {
				runtime.GC()
				var times [4]float64
				for step := range sizes {
					index := (step + run) % len(sizes)
					if run%2 != 0 {
						index = len(sizes) - 1 - index
					}
					times[index] = measure(index)
					samples[index] = append(samples[index], times[index])
				}
				growth[run] = scalingGrowth(times)
			}
			slices.Sort(growth)
			var ms [4]float64
			for index := range sizes {
				slices.Sort(samples[index])
				ms[index] = samples[index][len(samples[index])/2] / 1e6
			}
			median := growth[len(growth)/2]
			t.Logf("n%v=%0.3fms/%0.3fms/%0.3fms/%0.3fms fitted doubling=%0.3fx (median of five four-point runs)", sizes, ms[0], ms[1], ms[2], ms[3], median)
			if median > 2.3 {
				t.Fatalf("scaling regression: fitted per-doubling growth %.3fx (limit 2.3x)", median)
			}
		})
	}
}

func TestScalingSlopeRejectsQuadraticGrowth(t *testing.T) {
	if growth := scalingGrowth([4]float64{1, 2, 4, 8}); math.Abs(growth-2) > 1e-10 {
		t.Fatalf("linear growth %v", growth)
	}
	if growth := scalingGrowth([4]float64{1, 4, 16, 64}); growth < 3.99 {
		t.Fatalf("quadratic growth escaped: %v", growth)
	}
	// Measured pre-fix many-Var Prepare times, 250 through 2000 Vars.
	if growth := scalingGrowth([4]float64{19.264, 74.914, 297.775, 1195.292}); growth <= 2.3 {
		t.Fatalf("observed regression escaped: %v", growth)
	}
}
