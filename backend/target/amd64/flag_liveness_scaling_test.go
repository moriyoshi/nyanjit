package amd64

import (
	"os"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// flagStaircase builds a raw stream whose flag liveness crosses one back
// edge per step: a round-robin fixed point needs a pass per step, so its
// work grows with the square of m. Its last step has an addition whose flags
// only the graph decides and, with loop, a DEC RCX; JNZ countdown after it.
func flagStaircase(m int, loop bool) (*machine.Stream, []int, []bool) {
	s := deadFlagStream()
	labels, bound := make([]int, m+2), make([]bool, m+2)
	bind := func(l int) { labels[l], bound[l] = s.PC(), true }
	s.Append(machine.Instruction{Op: machine.OpJump, Imm: uint64(m)})
	bind(0)
	s.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchBelow, Imm: uint64(m + 1)})
	s.Append(machine.Instruction{Op: machine.OpReturn})
	for i := 1; i <= m; i++ {
		bind(i)
		if i == m {
			s.Append(machine.Instruction{Op: machine.OpBinary, Arithmetic: op.OpAdd, Bits: 64, A: 0, B: 1, C: 2})
			if loop {
				s.Append(machine.Instruction{Op: machine.OpDec, A: 1, B: 1})
				s.Append(machine.Instruction{Op: machine.OpJumpIf, Condition: op.BranchNotEqual, Imm: uint64(i)})
			}
		}
		s.Append(machine.Instruction{Op: machine.OpMove, A: 3, B: 4})
		s.Append(machine.Instruction{Op: machine.OpJump, Imm: uint64(i - 1)})
		s.Append(machine.Instruction{Op: machine.OpMove, A: 5, B: 6})
	}
	bind(m + 1)
	s.Append(machine.Instruction{Op: machine.OpReturn})
	return s, labels, bound
}

// incChain builds m INCs that each need a MOV, on a legacy stream that
// records INC as writing CF although it keeps it: each INC's flags then
// outlive the rest of the chain on a straight line.
func incChain(m int) (*machine.Stream, []int, []bool) {
	s := machine.NewStream(machine.StreamOptions{
		Size:       func(i machine.Instruction) int { return PhysicalSize(i, abi.StackPlatform, 0) },
		FlagEffect: FlagEffect,
	})
	for range m {
		s.Append(machine.Instruction{Op: machine.OpInc, A: 2, B: 6})
	}
	s.Append(machine.Instruction{Op: machine.OpReturn})
	return s, nil, nil
}

// checkLinear fails when pass takes much more than eight times as long on a
// stream eight times as large. A quadratic pass takes about 64 times as long.
func checkLinear(t *testing.T, name string, build func(m int) (*machine.Stream, []int, []bool), pass func(*machine.Stream, []int, []bool) error) {
	t.Helper()
	best := func(m int) time.Duration {
		var fastest time.Duration
		for run := range 3 {
			s, labels, bound := build(m)
			// Collect the fixture before timing. Otherwise GC of the stream
			// can dominate this short algorithmic growth check at large sizes.
			took := func() time.Duration {
				runtime.GC()
				previous := debug.SetGCPercent(-1)
				defer debug.SetGCPercent(previous)
				start := time.Now()
				if err := pass(s, labels, bound); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				runtime.KeepAlive(s)
				return time.Since(start)
			}()
			if run == 0 || took < fastest {
				fastest = took
			}
		}
		return max(fastest, time.Microsecond)
	}
	small, large := best(2000), best(16000)
	ratio := float64(large) / float64(small)
	t.Logf("%s: m=2000: %v; m=16000: %v; growth %.1fx", name, small, large, ratio)
	if ratio > 24 {
		t.Errorf("%s: %v for m=16000 against %v for m=2000 (ratio %.1f), want linear", name, large, small, ratio)
	}
}

// Flag liveness on raw streams is linear in the code (security audit
// 2026-10-25, L22).
func TestFlagLivenessIsLinear(t *testing.T) {
	if testing.Short() || os.Getenv("NYANJIT_PERF_SCALING") != "1" {
		t.Skip("set NYANJIT_PERF_SCALING=1 to run the native timing gate")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	elide := func(s *machine.Stream, labels []int, bound []bool) error {
		_, err := Policy{}.ElideDeadFlags(s, labels, bound)
		return err
	}
	checkLinear(t, "ElideDeadFlags staircase", func(m int) (*machine.Stream, []int, []bool) { return flagStaircase(m, false) }, elide)
	checkLinear(t, "ElideDeadFlags INC chain", incChain, elide)
	checkLinear(t, "FuseLoops staircase", func(m int) (*machine.Stream, []int, []bool) { return flagStaircase(m, true) }, func(s *machine.Stream, labels []int, bound []bool) error {
		_, err := fuseLoops(s, labels, bound)
		return err
	})
}

func BenchmarkElideDeadFlagsStaircase(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		s, labels, bound := flagStaircase(16000, false)
		b.StartTimer()
		if _, err := (Policy{}).ElideDeadFlags(s, labels, bound); err != nil {
			b.Fatal(err)
		}
	}
}
