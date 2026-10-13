package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/regalloc"
)

func TestRegisterGroupClaimAndRelease(t *testing.T) {
	state := regalloc.NewResidency(4, 3)
	pair, err := regalloc.PairedRegisters(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Claim(0, pair); err != nil {
		t.Fatal(err)
	}
	if state.First(0) != 2 || !state.Group(0).Contains(0) || state.Group(0).Contains(1) {
		t.Fatalf("pair order or membership lost: %+v", state.Group(0))
	}
	if err := state.Claim(1, regalloc.SingleRegister(0)); !errors.Is(err, ErrBackendLimit) {
		t.Fatalf("overlapping claim = %v, want ErrBackendLimit", err)
	}
	if state.Owners[2] != 0 || state.Owners[0] != 0 || state.Group(1).Words != 0 {
		t.Fatalf("failed claim changed residency: %+v", state)
	}
	state.Release(0)
	if state.Owners[2] != -1 || state.Owners[0] != -1 || state.Group(0).Words != 0 {
		t.Fatalf("release left part of pair resident: %+v", state)
	}
	if _, err := regalloc.PairedRegisters(1, 1); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("aliasing pair = %v, want ErrInvalidFunction", err)
	}
}

func TestRegisterGroupEvictionIsAtomic(t *testing.T) {
	state := regalloc.NewResidency(3, 3)
	pair, _ := regalloc.PairedRegisters(0, 2)
	if err := state.Claim(0, pair); err != nil {
		t.Fatal(err)
	}
	if err := state.Claim(1, regalloc.SingleRegister(1)); err != nil {
		t.Fatal(err)
	}
	var evicted []int
	got, err := state.Reserve(1, map[int]bool{1: true}, regalloc.SingleRegister(2),
		func(id int) int { return id },
		func(id int, group regalloc.Group) error {
			evicted = append(evicted, id)
			if group != pair {
				t.Fatalf("evicted group = %+v, want %+v", group, pair)
			}
			return nil
		})
	if err != nil || got != regalloc.SingleRegister(2) {
		t.Fatalf("reserved %+v, %v; want register 2", got, err)
	}
	if len(evicted) != 1 || evicted[0] != 0 || state.Owners[0] != -1 || state.Owners[2] != -1 || state.Owners[1] != 1 {
		t.Fatalf("partial pair eviction: state=%+v, evicted=%v", state, evicted)
	}
	if err := state.Claim(2, got); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterGroupPinnedAndFailedSpill(t *testing.T) {
	state := regalloc.NewResidency(2, 2)
	pair, _ := regalloc.PairedRegisters(0, 1)
	if err := state.Claim(0, pair); err != nil {
		t.Fatal(err)
	}
	spillCalled := false
	_, err := state.Reserve(1, map[int]bool{0: true}, regalloc.Group{},
		func(int) int { return 1 }, func(int, regalloc.Group) error { spillCalled = true; return nil })
	if !errors.Is(err, ErrBackendLimit) || spillCalled || state.Group(0) != pair {
		t.Fatalf("pinned pair was evicted: state=%+v, spill=%v, err=%v", state, spillCalled, err)
	}
	want := errors.New("spill failed")
	_, err = state.Reserve(1, nil, regalloc.Group{}, func(int) int { return 1 },
		func(int, regalloc.Group) error { return want })
	if !errors.Is(err, want) || state.Group(0) != pair || state.Owners[0] != 0 || state.Owners[1] != 0 {
		t.Fatalf("failed spill changed group: state=%+v, err=%v", state, err)
	}
}

func TestRegisterGroupReservesNonadjacentPair(t *testing.T) {
	state := regalloc.NewResidency(4, 3)
	if err := state.Claim(0, regalloc.SingleRegister(0)); err != nil {
		t.Fatal(err)
	}
	if err := state.Claim(1, regalloc.SingleRegister(2)); err != nil {
		t.Fatal(err)
	}
	want, _ := regalloc.PairedRegisters(3, 1)
	got, err := state.Reserve(2, nil, want, func(int) int { return 0 },
		func(int, regalloc.Group) error { t.Fatal("free pair unexpectedly evicted a value"); return nil })
	if err != nil || got != want {
		t.Fatalf("reserved pair = %+v, %v; want %+v", got, err, want)
	}
	if err := state.Claim(2, got); err != nil {
		t.Fatal(err)
	}
	if state.Owners[3] != 2 || state.Owners[1] != 2 || state.First(2) != 3 {
		t.Fatalf("pair order or ownership changed: %+v", state)
	}
}

func TestSplitIntegerWords(t *testing.T) {
	const value = uint64(0xfedcba9876543210)
	parts, err := regalloc.SplitIntegerWords(value, 32, 2)
	if err != nil || parts != [2]uint64{0x76543210, 0xfedcba98} {
		t.Fatalf("32-bit pair = %#v, %v", parts, err)
	}
	parts, err = regalloc.SplitIntegerWords(value, 64, 1)
	if err != nil || parts != [2]uint64{value, 0} {
		t.Fatalf("64-bit single = %#v, %v", parts, err)
	}
	if _, err := regalloc.SplitIntegerWords(value, 64, 2); !errors.Is(err, ErrBackendLimit) {
		t.Fatalf("invalid 64-bit pair = %v", err)
	}
}

func TestIntegerRegisterWords(t *testing.T) {
	for _, tc := range []struct {
		bits, registerBits uint8
		want               uint8
	}{
		{8, 32, 1}, {32, 32, 1}, {64, 32, 2},
		{8, 64, 1}, {64, 64, 1}, {128, 64, 2},
	} {
		got, err := regalloc.IntegerRegisterWords(tc.bits, tc.registerBits)
		if err != nil || got != tc.want {
			t.Fatalf("%d bits / %d-bit register = %d, %v; want %d", tc.bits, tc.registerBits, got, err, tc.want)
		}
	}
	for _, bits := range []uint8{0, 96, 128} {
		if _, err := regalloc.IntegerRegisterWords(bits, 32); !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("%d bits / 32-bit register = %v, want ErrBackendLimit", bits, err)
		}
	}
}
