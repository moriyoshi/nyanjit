package machine

import "testing"

func TestStreamSizeCacheAndLayoutFacts(t *testing.T) {
	sizes, facts := 0, 0
	s := NewStream(StreamOptions{
		Size: func(i Instruction) int {
			sizes++
			if i.Long {
				return 8
			}
			return 4
		},
		FlagEffect: func(Instruction) bool { t.Fatal("exact facts should be queried only once"); return false },
		FlagFacts: func(i Instruction) FlagState {
			facts++
			return FlagState{Modified: FlagZero, Zero: FlagFact{Valid: true, Width: 64}}
		},
		ExactFlagFacts: true,
	})
	s.Reserve(2)
	s.Append(Instruction{Op: OpBinary})
	s.BindFlagValue(9, 2, 3, 0, false)
	if s.SizeAt(0) != 4 || s.SizeAt(0) != 4 || sizes != 1 || facts != 1 {
		t.Fatalf("sizes%d facts%d", sizes, facts)
	}
	s.ReplaceKeepingFacts([]Instruction{{Op: OpBinary, Long: true}})
	if s.PC() != 8 || s.SizeAt(0) != 8 || !s.Flags().Zero.MatchesValue(9) || facts != 2 {
		t.Fatal("layout lost size or value facts")
	}
	s.Replace([]Instruction{{Op: OpBinary}})
	if s.PC() != 4 || s.Flags().Zero.Valid {
		t.Fatal("semantic replacement retained stale value facts")
	}
}

func TestLayoutRewriteRecomputesFlagSummary(t *testing.T) {
	for _, mode := range []string{"exact", "union", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			options := StreamOptions{Size: func(Instruction) int { return 4 }, FlagEffect: func(i Instruction) bool { return i.Op == OpBinary || mode == "union" && i.Op == OpJumpIf }}
			if mode != "legacy" {
				options.FlagFacts = func(i Instruction) FlagState {
					if i.Op == OpBinary {
						return FlagState{Modified: FlagZero}
					}
					return FlagState{}
				}
			}
			options.ExactFlagFacts = mode == "exact"
			s := NewStream(options)
			s.Append(Instruction{Op: OpBinary})
			s.Append(Instruction{Op: OpJumpIf})
			if !s.FlagsClobbered() {
				t.Fatal("missing original flag writer")
			}
			s.ReplaceKeepingFacts([]Instruction{{Op: OpJumpIf}})
			if s.FlagsClobbered() != (mode == "union") {
				t.Fatalf("stale/omitted flag summary after removal: %v", s.FlagsClobbered())
			}
			if s.PC() != 4 || s.SizeAt(0) != 4 {
				t.Fatal("layout size cache lost")
			}
			s.ReplaceKeepingFacts([]Instruction{{Op: OpMove}})
			if s.FlagsClobbered() {
				t.Fatal("removed sole flag writer remains in report")
			}
		})
	}
}

func TestReplaceAtMeasuresOneInstruction(t *testing.T) {
	sizes := 0
	s := NewStream(StreamOptions{
		Size: func(i Instruction) int {
			sizes++
			if i.Long {
				return 8
			}
			return 4
		},
		FlagFacts: func(i Instruction) FlagState {
			if i.Op == OpBinary && !i.PreserveFlags {
				return FlagState{Modified: FlagZero, Zero: FlagFact{Valid: true, Width: 64}}
			}
			return FlagState{}
		},
		ExactFlagFacts: true,
	})
	s.Append(Instruction{Op: OpBinary})
	s.BindFlagValue(9, 2, 3, 0, false)
	s.Append(Instruction{Op: OpMove})
	s.Append(Instruction{Op: OpReturn})
	borrowed := s.Instructions()
	sizes = 0
	if n := s.ReplaceAt(1, Instruction{Op: OpMove, Long: true}); n != 8 {
		t.Fatalf("ReplaceAt size %d", n)
	}
	if sizes != 1 || s.PC() != 16 || s.SizeAt(1) != 8 || s.Layout().PC(2) != 12 {
		t.Fatalf("measured %d times, PC %d, size %d", sizes, s.PC(), s.SizeAt(1))
	}
	if !borrowed[1].Long {
		t.Fatal("borrowed instructions do not show the rewrite")
	}
	if !s.Flags().Zero.MatchesValue(9) {
		t.Fatal("layout rewrite dropped flag facts")
	}
	s.ReplaceAt(0, Instruction{Op: OpBinary, PreserveFlags: true})
	if s.FlagsClobbered() {
		t.Fatal("flag summary kept a removed flag writer")
	}
	s.ForgetFlags()
	if s.Flags().Zero.Valid {
		t.Fatal("ForgetFlags kept facts after the rewrite")
	}
	s.ReplaceAt(2, Instruction{Op: OpBinary})
	if !s.FlagsClobbered() {
		t.Fatal("flag summary missed a new flag writer")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("ReplaceAt accepted an index out of range")
		}
	}()
	s.ReplaceAt(3, Instruction{Op: OpReturn})
}

func TestReplaceAtMatchesLayoutRewrite(t *testing.T) {
	for _, mode := range []string{"exact", "union", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			options := StreamOptions{Size: func(i Instruction) int { return 4 + int(i.Imm) }, FlagEffect: func(i Instruction) bool { return i.Op == OpBinary || mode == "union" && i.Op == OpJumpIf }}
			if mode != "legacy" {
				options.FlagFacts = func(i Instruction) FlagState {
					if i.Op == OpBinary {
						return FlagState{Modified: FlagZero}
					}
					return FlagState{}
				}
			}
			options.ExactFlagFacts = mode == "exact"
			code := []Instruction{{Op: OpBinary}, {Op: OpJumpIf}, {Op: OpBinary}}
			rewritten := []Instruction{{Op: OpJumpIf, Imm: 2}, {Op: OpJumpIf}, {Op: OpMove, Imm: 4}}
			in, whole := NewStream(options), NewStream(options)
			for _, i := range code {
				in.Append(i)
				whole.Append(i)
			}
			for n, i := range rewritten {
				in.ReplaceAt(n, i)
			}
			whole.ReplaceKeepingFacts(rewritten)
			if in.FlagsClobbered() != whole.FlagsClobbered() || in.PC() != whole.PC() || in.Flags().Modified != whole.Flags().Modified {
				t.Fatalf("ReplaceAt: clobbered %v PC %d; ReplaceKeepingFacts: clobbered %v PC %d",
					in.FlagsClobbered(), in.PC(), whole.FlagsClobbered(), whole.PC())
			}
			for n := range rewritten {
				if in.SizeAt(n) != whole.SizeAt(n) {
					t.Fatalf("size %d: %d, want %d", n, in.SizeAt(n), whole.SizeAt(n))
				}
			}
		})
	}
}

func TestModifiedFlagsAtFollowsEveryRewrite(t *testing.T) {
	for _, mode := range []string{"exact", "union", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			options := StreamOptions{Size: func(Instruction) int { return 4 }, FlagEffect: func(i Instruction) bool { return i.Op == OpBinary || mode == "union" && i.Op == OpJumpIf }}
			if mode != "legacy" {
				options.FlagFacts = func(i Instruction) FlagState {
					switch {
					case i.Op == OpBinary && i.PreserveFlags:
						return FlagState{}
					case i.Op == OpBinary:
						return FlagState{Modified: FlagZero | FlagCarry}
					case i.Op == OpInc:
						return FlagState{Modified: FlagSign | FlagZero}
					}
					return FlagState{}
				}
			}
			options.ExactFlagFacts = mode == "exact"
			var w Workspace
			for _, s := range []*Stream{NewStream(options), w.Stream(options)} {
				check := func(step string) {
					t.Helper()
					for n, i := range s.Instructions() {
						if got, want := s.ModifiedFlagsAt(n), s.ModifiedFlags(i); got != want {
							t.Fatalf("%s: instruction %d: ModifiedFlagsAt %v, ModifiedFlags %v", step, n, got, want)
						}
					}
				}
				s.Reserve(2)
				for _, i := range []Instruction{{Op: OpBinary}, {Op: OpJumpIf}, {Op: OpInc}, {Op: OpMove}} {
					s.Append(i)
				}
				check("Append")
				s.ReplaceAt(0, Instruction{Op: OpBinary, PreserveFlags: true})
				s.ReplaceAt(3, Instruction{Op: OpInc})
				check("ReplaceAt")
				s.ReplaceKeepingFacts([]Instruction{{Op: OpInc}, {Op: OpBinary}, {Op: OpMove}})
				check("ReplaceKeepingFacts")
				s.Replace([]Instruction{{Op: OpMove}, {Op: OpBinary}})
				check("Replace")
			}
			w.Release()
			s := w.Stream(options)
			s.Append(Instruction{Op: OpMove})
			if s.ModifiedFlagsAt(0) != 0 {
				t.Fatal("a new lease kept an earlier stream's marks")
			}
		})
	}
}
