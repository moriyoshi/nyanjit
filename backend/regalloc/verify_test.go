package regalloc

import "testing"

// faultyMover forwards to a Mover but drops the moves drop selects.
type faultyMover struct {
	m     Mover
	n     int
	drop  func(kind recordedKind, n int) bool
	moved bool
}

func (f *faultyMover) keep(kind recordedKind) bool {
	f.n++
	if f.drop(kind, f.n) {
		f.moved = true
		return false
	}
	return true
}

func (f *faultyMover) Move(value int, to, from Group) {
	if f.keep(recMove) {
		f.m.Move(value, to, from)
	}
}
func (f *faultyMover) Spill(value int, slot int, from Group) {
	if f.keep(recSpill) {
		f.m.Spill(value, slot, from)
	}
}
func (f *faultyMover) Reload(value int, to Group, slot int) {
	if f.keep(recReload) {
		f.m.Reload(value, to, slot)
	}
}
func (f *faultyMover) LoadIncoming(value int, to Group, offsets [2]int) {
	if f.keep(recIncoming) {
		f.m.LoadIncoming(value, to, offsets)
	}
}
func (f *faultyMover) Rematerialize(value int, to Group) error {
	if f.keep(recRemat) {
		return f.m.Rematerialize(value, to)
	}
	return nil
}

// faulty is an Allocator that breaks another: it drops moves, or reports
// the wrong registers for one operand.
type faulty struct {
	Allocator
	mover    faultyMover
	drop     func(kind recordedKind, n int) bool
	operand  [2]int // position and index to misreport, or -1
	misreads bool
	memory   bool // misreport every slot of a Memory use
}

func (f *faulty) Memory(position, index int) ValueLocation {
	loc := f.Allocator.Memory(position, index)
	if f.memory && loc.Slot >= 0 {
		f.misreads = true
		loc.Slot ^= 1
	}
	return loc
}

func (f *faulty) Allocate(config Config, fn Function, m Mover) error {
	f.mover = faultyMover{m: m, drop: f.drop}
	return f.Allocator.Allocate(config, fn, &f.mover)
}

func (f *faulty) Operand(position, index int) Group {
	g := f.Allocator.Operand(position, index)
	if position == f.operand[0] && index == f.operand[1] && g.Words == 1 {
		f.misreads = true
		g.Registers[0] ^= 1
	}
	return g
}

// Verify must reject an allocation that loses values. Dropping a move or
// misreporting an operand does not always matter (the value may be dead or
// still there), so it must catch most faults rather than every one.
func TestVerifyCatchesFaults(t *testing.T) {
	never := func(recordedKind, int) bool { return false }
	only := func(kind recordedKind) func(recordedKind, int) bool {
		return func(k recordedKind, _ int) bool { return k == kind }
	}
	cases := []struct {
		name    string
		drop    func(recordedKind, int) bool
		operand bool
	}{
		{"drop reloads", only(recReload), false},
		{"drop spills", only(recSpill), false},
		{"drop moves", only(recMove), false},
		{"misreport an operand", never, true},
		{"misreport a memory use", never, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			faults, caught := 0, 0
			for seed := range uint64(400) {
				g := generate(seed)
				a := &faulty{Allocator: &Batch{}, drop: c.drop, operand: [2]int{-1, -1}, memory: c.name == "misreport a memory use"}
				if c.operand {
					a.operand = [2]int{int(seed % 8), 0}
				}
				err := Verify(g.config, g.function(), a)
				if !a.mover.moved && !a.misreads {
					if err != nil && (&Batch{}).Allocate(g.config, g.function(), nil) == nil {
						t.Fatalf("seed %d: no fault, but %v", seed, err)
					}
					continue
				}
				faults++
				if err != nil {
					caught++
				}
			}
			if faults == 0 || caught*4 < faults*3 {
				t.Fatalf("caught %d of %d faults", caught, faults)
			}
			t.Logf("caught %d of %d faults", caught, faults)
		})
	}
}

// noScopes hides a function's scopes from the allocator that is checked.
type noScopes struct{ Function }

func (noScopes) Scopes() int     { return 0 }
func (noScopes) Scope(int) Scope { return Scope{} }

// blind allocates without scopes but is verified against them.
type blind struct{ Allocator }

func (b blind) Allocate(config Config, f Function, m Mover) error {
	return b.Allocator.Allocate(config, noScopes{f}, m)
}

// Verify must catch an allocator that ignores the scopes' reserved
// registers: whenever the simulator, which clobbers them during each
// instruction, sees a value lost, Verify must reject the allocation too.
func TestVerifyCatchesIgnoredScopes(t *testing.T) {
	for _, c := range testAllocators() {
		t.Run(c.name, func(t *testing.T) {
			broken, caught := 0, 0
			for seed := range uint64(1500) {
				g := generate(seed)
				if len(g.scopes) == 0 {
					continue
				}
				w, err := recordWalk(g.config, g.function(), blind{c.a})
				if err != nil {
					continue
				}
				lost := false
				for run := range uint64(4) {
					m := &machineState{t: t, g: g, w: w, quiet: true}
					m.run(seed*16 + run)
					lost = lost || m.failed
				}
				rejected := Verify(g.config, g.function(), blind{c.a}) != nil
				if lost {
					broken++
					if !rejected {
						t.Fatalf("seed %d: the simulator loses a value Verify accepts", seed)
					}
				}
				if rejected {
					caught++
				}
			}
			if broken == 0 {
				t.Fatal("ignoring scopes never lost a value")
			}
			t.Logf("%d allocations lose values, Verify rejects %d", broken, caught)
		})
	}
}
