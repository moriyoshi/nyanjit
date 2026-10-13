package machine

import (
	"os"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestARMZeroLoopPreservesLiveFlags(t *testing.T) {
	for _, liveCarry := range []bool{false, true} {
		s := NewStream(StreamOptions{Size: func(i Instruction) int {
			if i.Op == OpBranchZero && i.Long {
				return 8
			}
			return 4
		}, FlagEffect: func(i Instruction) bool { return i.Op == OpDec && !i.PreserveFlags }, FlagFacts: func(i Instruction) FlagState {
			if i.Op == OpDec && !i.PreserveFlags {
				return FlagState{Modified: FlagSign | FlagZero | FlagCarry}
			}
			return FlagState{}
		}})
		s.Append(Instruction{Op: OpMove, A: 2, B: 3})
		s.Append(Instruction{Op: OpDec, A: 2, B: 2})
		s.Append(Instruction{Op: OpJumpIf, Condition: op.BranchNotEqual, Imm: 0})
		if liveCarry {
			s.Append(Instruction{Op: OpWordCarry, CarryMode: CarryFlags})
		}
		s.Append(Instruction{Op: OpReturn})
		_, err := FuseZeroLoops(s, []int{0}, []bool{true}, "arm64")
		if err != nil {
			t.Fatal(err)
		}
		fused := s.Instructions()[1].PreserveFlags
		if fused == liveCarry {
			t.Fatalf("carry live=%v fused=%v", liveCarry, fused)
		}
		if fused && s.Instructions()[2].Op != OpBranchZero {
			t.Fatal("missing direct branch")
		}
	}
}

// armDecStream is a stream whose decrements write every tracked flag unless
// they preserve flags.
func armDecStream() *Stream {
	return NewStream(StreamOptions{Size: func(Instruction) int { return 4 }, FlagFacts: func(i Instruction) FlagState {
		if i.Op == OpDec && !i.PreserveFlags {
			return FlagState{Modified: FlagSign | FlagZero | FlagCarry}
		}
		return FlagState{}
	}, ExactFlagFacts: true})
}

// On arm64 a decrement without flags writes SP when its register is 31, so
// a countdown in register 31 keeps its flag-writing form (security audit
// 2026-10-25, L24).
func TestARMZeroLoopSkipsRegister31(t *testing.T) {
	for _, register := range []backend.Register{30, 31} {
		s := armDecStream()
		s.Append(Instruction{Op: OpDec, A: register, B: register})
		s.Append(Instruction{Op: OpJumpIf, Condition: op.BranchNotEqual, Imm: 0})
		s.Append(Instruction{Op: OpReturn})
		if _, err := FuseZeroLoops(s, []int{0}, []bool{true}, "arm64"); err != nil {
			t.Fatal(err)
		}
		fused := s.Instructions()[1].Op == OpBranchZero
		if fused != (register != 31) || s.Instructions()[0].PreserveFlags != fused {
			t.Fatalf("register %d: stream %v", register, s.Instructions())
		}
	}
}

// zeroLoopStaircase builds a stream whose flag liveness crosses one back
// edge per step, ending in a countdown whose branch keeps the flags live.
func zeroLoopStaircase(m int) (*Stream, []int, []bool) {
	s := armDecStream()
	labels, bound := make([]int, m+2), make([]bool, m+2)
	bind := func(l int) { labels[l], bound[l] = s.PC(), true }
	s.Append(Instruction{Op: OpJump, Imm: uint64(m)})
	bind(0)
	s.Append(Instruction{Op: OpJumpIf, Condition: op.BranchBelow, Imm: uint64(m + 1)})
	s.Append(Instruction{Op: OpReturn})
	for i := 1; i <= m; i++ {
		bind(i)
		if i == m {
			s.Append(Instruction{Op: OpDec, A: 2, B: 2})
			s.Append(Instruction{Op: OpJumpIf, Condition: op.BranchNotEqual, Imm: uint64(i)})
		}
		s.Append(Instruction{Op: OpMove, A: 3, B: 4})
		s.Append(Instruction{Op: OpJump, Imm: uint64(i - 1)})
		s.Append(Instruction{Op: OpMove, A: 5, B: 6})
	}
	bind(m + 1)
	s.Append(Instruction{Op: OpReturn})
	return s, labels, bound
}

// Flag liveness on raw streams is linear in the code (security audit
// 2026-10-25, L22): eight times the staircase takes about eight times as
// long, where a round-robin fixed point takes about 64 times.
func TestFuseZeroLoopsLivenessIsLinear(t *testing.T) {
	if testing.Short() || os.Getenv("NYANJIT_PERF_SCALING") != "1" {
		t.Skip("set NYANJIT_PERF_SCALING=1 to run the native timing gate")
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	best := func(m int) time.Duration {
		var fastest time.Duration
		for run := range 3 {
			s, labels, bound := zeroLoopStaircase(m)
			// Stream construction leaves a large pointer-bearing working set.
			// Finish its collection before timing, and exclude concurrent GC
			// from this algorithmic growth check. Restore GC after each sample
			// so the next fixture cannot accumulate unbounded garbage.
			took := func() time.Duration {
				runtime.GC()
				previous := debug.SetGCPercent(-1)
				defer debug.SetGCPercent(previous)
				start := time.Now()
				if _, err := FuseZeroLoops(s, labels, bound, "arm64"); err != nil {
					t.Fatal(err)
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
	t.Logf("m=2000: %v; m=16000: %v; growth %.1fx", small, large, ratio)
	if ratio > 24 {
		t.Errorf("%v for m=16000 against %v for m=2000 (ratio %.1f), want linear", large, small, ratio)
	}
}

func TestRISCZeroLoopEliminatesComparePseudo(t *testing.T) {
	s := NewStream(StreamOptions{Size: func(i Instruction) int {
		if i.Op == OpCompareImmediate {
			return 0
		}
		return 4
	}, FlagEffect: func(Instruction) bool { return false }})
	s.Append(Instruction{Op: OpDec, A: 10, B: 10})
	s.Append(Instruction{Op: OpCompareImmediate, A: 10})
	s.Append(Instruction{Op: OpJumpIf, Condition: op.BranchNotEqual, Imm: 0})
	s.Append(Instruction{Op: OpReturn})
	labels, err := FuseZeroLoops(s, []int{0}, []bool{true}, "riscv64")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Instructions()) != 3 || s.Instructions()[1].Op != OpBranchZero || labels[0] != 0 || s.PC() != 12 {
		t.Fatalf("stream=%v labels=%v PC=%d", s.Instructions(), labels, s.PC())
	}
}
