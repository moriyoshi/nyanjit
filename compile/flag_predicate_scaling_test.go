package compile

import (
	"math"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

// signAliasProgram defines 8n values, then n sign extensions that alias a
// 32-bit sign fact: the worst case for a per-alias producer lookup.
func signAliasProgram(n int) (machineProgram, flagState) {
	prefix := 8 * n
	program := machineProgram{Values: make([]machine.SelectedValue, prefix+n)}
	aliases := make([]int, 0, n)
	for value := range prefix + n {
		op := machine.SelectedNarrow
		if value >= prefix {
			op = machine.SelectedSignExtend
			aliases = append(aliases, value)
		}
		program.Instructions = append(program.Instructions, machine.SelectedInstruction{Op: op, Dst: value})
	}
	flags := flagState{Sign: machine.FlagFact{Valid: true, Width: 32, Value: 0, Aliases: aliases}}
	return program, flags
}

// TestRegisterImageFlagsScaling checks that the sign filter is linear in the
// block's instructions plus the fact's aliases. Its sizes span 8x, so the
// limit matches the scaling gate's 2.3x per doubling.
func TestRegisterImageFlagsScaling(t *testing.T) {
	if os.Getenv("NYANJIT_PERF_SCALING") != "1" {
		t.Skip("set NYANJIT_PERF_SCALING=1 to run the native timing gate")
	}
	measure := func(n int) float64 {
		program, flags := signAliasProgram(n)
		var samples []float64
		for range 7 {
			start := time.Now()
			runs := 0
			for runs == 0 || time.Since(start) < 40*time.Millisecond {
				if got := registerImageFlags(flags, program, 64); len(got.Sign.Aliases) != n-1 {
					t.Fatalf("n=%d kept %d aliases", n, len(got.Sign.Aliases))
				}
				runs++
			}
			samples = append(samples, float64(time.Since(start).Nanoseconds())/float64(runs))
		}
		slices.Sort(samples)
		return samples[len(samples)/2]
	}
	small, large := measure(500), measure(4000)
	growth := math.Cbrt(large / small)
	t.Logf("n500=%.1fus n4000=%.1fus per-doubling=%.3fx", small/1e3, large/1e3, growth)
	if growth > 2.3 {
		t.Fatalf("scaling regression: per-doubling growth %.3fx (limit 2.3x)", growth)
	}
}
