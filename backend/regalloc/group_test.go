package regalloc

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
)

// pairIndex returns a two-word group at low and high.
func pairIndex(low, high int) indexGroup {
	return indexGroup{Indices: [2]int{low, high}, Words: 2}
}

func TestGroupHelpers(t *testing.T) {
	pair, err := PairedRegisters(backend.VectorRegister(2), 0)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := pair.First(); !ok || r != backend.VectorRegister(2) || !pair.Contains(0) || pair.Contains(1) {
		t.Fatalf("pair order or membership lost: %+v", pair)
	}
	if _, ok := (Group{}).First(); ok || (Group{}).Contains(0) {
		t.Fatal("the empty group holds a register")
	}
	if SingleRegister(3) != (Group{Registers: [2]backend.Register{3}, Words: 1}) {
		t.Fatal("a single group keeps a second register")
	}
	if _, err := PairedRegisters(1, 1); !errors.Is(err, backend.ErrInvalidFunction) {
		t.Fatalf("aliasing pair = %v, want ErrInvalidFunction", err)
	}
}

func TestPins(t *testing.T) {
	var p Pins
	pair, _ := PairedRegisters(3, 200)
	p.Add(pair)
	p.AddRegister(backend.VectorRegister(1))
	if !p.Contains(3) || !p.Contains(200) || !p.Contains(backend.VectorRegister(1)) || p.Contains(4) {
		t.Fatalf("pins %+v", p)
	}
	p.Remove(SingleRegister(200))
	if p.Contains(200) || !p.Contains(3) {
		t.Fatalf("Remove: %+v", p)
	}
	// Registers outside Config.Registers have no index and are dropped.
	f := newRegisterIndex(Config{Registers: backend.NewRegisterSet(backend.VectorRegister(1), 7, 3)})
	if got := f.pins(p); got != 0b101 {
		t.Fatalf("index mask %03b, want 101", got)
	}
	if g, ok := f.indices(pair); ok {
		t.Fatalf("a register outside the set got an index: %+v", g)
	}
	if g, ok := f.indices(SingleRegister(3)); !ok || g != singleIndex(2) || f.group(g) != SingleRegister(3) {
		t.Fatalf("indices = %+v, %v", g, ok)
	}
}

func TestOccupancyClaimAndRelease(t *testing.T) {
	state := newOccupancy(4, 3)
	pair := pairIndex(2, 0)
	if err := state.claim(0, pair, Group{}); err != nil {
		t.Fatal(err)
	}
	if state.held[0].group.First() != 2 || !state.held[0].group.Contains(0) || state.held[0].group.Contains(1) {
		t.Fatalf("pair order or membership lost: %+v", state.held[0].group)
	}
	if err := state.claim(1, singleIndex(0), Group{}); !errors.Is(err, backend.ErrBackendLimit) {
		t.Fatalf("overlapping claim = %v, want ErrBackendLimit", err)
	}
	if err := state.claim(1, pairIndex(1, 1), Group{}); !errors.Is(err, backend.ErrBackendLimit) {
		t.Fatalf("aliasing pair = %v, want ErrBackendLimit", err)
	}
	if state.owners[2] != 0 || state.owners[0] != 0 || state.held[1].group.Words != 0 {
		t.Fatalf("failed claim changed occupancy: %+v", state)
	}
	state.release(0)
	if state.owners[2] != -1 || state.owners[0] != -1 || state.held[0].group.Words != 0 {
		t.Fatalf("release left part of pair resident: %+v", state)
	}
}

func TestOccupancyEvictionIsAtomic(t *testing.T) {
	state := newOccupancy(3, 3)
	pair := pairIndex(0, 2)
	if err := state.claim(0, pair, Group{}); err != nil {
		t.Fatal(err)
	}
	if err := state.claim(1, singleIndex(1), Group{}); err != nil {
		t.Fatal(err)
	}
	var evicted []int
	got, err := state.reserve(1, 1<<1, singleIndex(2),
		func(id int) int { return id },
		func(id int, group indexGroup) error {
			evicted = append(evicted, id)
			if group != pair {
				t.Fatalf("evicted group = %+v, want %+v", group, pair)
			}
			return nil
		})
	if err != nil || got != singleIndex(2) {
		t.Fatalf("reserved %+v, %v; want register 2", got, err)
	}
	if len(evicted) != 1 || evicted[0] != 0 || state.owners[0] != -1 || state.owners[2] != -1 || state.owners[1] != 1 {
		t.Fatalf("partial pair eviction: state=%+v, evicted=%v", state, evicted)
	}
	if err := state.claim(2, got, Group{}); err != nil {
		t.Fatal(err)
	}
}

func TestOccupancyPinnedAndFailedSpill(t *testing.T) {
	state := newOccupancy(2, 2)
	pair := pairIndex(0, 1)
	if err := state.claim(0, pair, Group{}); err != nil {
		t.Fatal(err)
	}
	spillCalled := false
	_, err := state.reserve(1, 1<<0, indexGroup{},
		func(int) int { return 1 }, func(int, indexGroup) error { spillCalled = true; return nil })
	if !errors.Is(err, backend.ErrBackendLimit) || spillCalled || state.held[0].group != pair {
		t.Fatalf("pinned pair was evicted: state=%+v, spill=%v, err=%v", state, spillCalled, err)
	}
	want := errors.New("spill failed")
	_, err = state.reserve(1, 0, indexGroup{}, func(int) int { return 1 },
		func(int, indexGroup) error { return want })
	if !errors.Is(err, want) || state.held[0].group != pair || state.owners[0] != 0 || state.owners[1] != 0 {
		t.Fatalf("failed spill changed group: state=%+v, err=%v", state, err)
	}
}

func TestOccupancyReservesNonadjacentPair(t *testing.T) {
	state := newOccupancy(4, 3)
	if err := state.claim(0, singleIndex(0), Group{}); err != nil {
		t.Fatal(err)
	}
	if err := state.claim(1, singleIndex(2), Group{}); err != nil {
		t.Fatal(err)
	}
	want := pairIndex(3, 1)
	got, err := state.reserve(2, 0, want, func(int) int { return 0 },
		func(int, indexGroup) error { t.Fatal("free pair unexpectedly evicted a value"); return nil })
	if err != nil || got != want {
		t.Fatalf("reserved pair = %+v, %v; want %+v", got, err, want)
	}
	if err := state.claim(2, got, Group{}); err != nil {
		t.Fatal(err)
	}
	if state.owners[3] != 2 || state.owners[1] != 2 || state.held[2].group.First() != 3 {
		t.Fatalf("pair order or ownership changed: %+v", state)
	}
}

func TestReserveVictimTieUsesLowestValueID(t *testing.T) {
	for _, pinned := range []uint64{0, 1 << 2} {
		state := newOccupancy(4, 20)
		for reg, value := range []int{17, 3, 1, 8} {
			if err := state.claim(value, singleIndex(reg), Group{}); err != nil {
				t.Fatal(err)
			}
		}
		want := 1
		if pinned != 0 {
			want = 3
		}
		victim := -1
		_, err := state.reserve(1, pinned, indexGroup{}, func(int) int { return 100 }, func(id int, _ indexGroup) error { victim = id; return nil })
		if err != nil || victim != want {
			t.Fatalf("victim%d err%v want%d", victim, err, want)
		}
	}
}

func TestReservePairedVictimEvictedOnceInValueOrder(t *testing.T) {
	state := newOccupancy(4, 20)
	for id, g := range map[int]indexGroup{2: pairIndex(3, 0), 6: singleIndex(1), 1: singleIndex(2)} {
		if err := state.claim(id, g, Group{}); err != nil {
			t.Fatal(err)
		}
	}
	var victims []int
	got, err := state.reserve(2, 0, indexGroup{}, func(int) int { return 10 }, func(id int, _ indexGroup) error { victims = append(victims, id); return nil })
	if err != nil || !slices.Equal(victims, []int{1, 2}) || got.Indices != [2]int{0, 2} {
		t.Fatalf("group%v victims%v err%v", got, victims, err)
	}
}
