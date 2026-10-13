package regalloc

import (
	"errors"
	"fmt"
	"github.com/moriyoshi/nyanjit/backend"
	"math"
	"reflect"
	"slices"
	"testing"
)

// recorder is a Mover that records what the Residency asks for.
type recorder struct{ log []string }

func (r *recorder) Move(value int, to, from Group) {
	r.log = append(r.log, fmt.Sprintf("move v%d r%d<-r%d", value, first(to), first(from)))
}
func (r *recorder) Spill(value int, slot int, from Group) {
	r.log = append(r.log, fmt.Sprintf("spill v%d r%d s%d", value, first(from), slot))
}
func (r *recorder) Reload(value int, to Group, slot int) {
	r.log = append(r.log, fmt.Sprintf("reload v%d r%d s%d", value, first(to), slot))
}
func (r *recorder) LoadIncoming(value int, to Group, offsets [2]int) {
	r.log = append(r.log, fmt.Sprintf("incoming v%d r%d @%d", value, first(to), offsets[0]))
}
func (r *recorder) Rematerialize(value int, to Group) error {
	r.log = append(r.log, fmt.Sprintf("const v%d r%d", value, first(to)))
	return nil
}

// first returns the register holding group's low word, or -1.
func first(group Group) int {
	if r, ok := group.First(); ok {
		return int(r)
	}
	return -1
}

// pins pins registers.
func pins(registers ...backend.Register) Pins {
	var p Pins
	for _, r := range registers {
		p.AddRegister(r)
	}
	return p
}

// testProgram is a Program of per-position input lists.
type testProgram struct {
	values []Value
	inputs [][]int
}

func (p testProgram) Values() int                          { return len(p.values) }
func (p testProgram) Value(id int) Value                   { return p.values[id] }
func (p testProgram) Positions() int                       { return len(p.inputs) }
func (p testProgram) Inputs(position int, dst []int) []int { return append(dst, p.inputs[position]...) }

// sequence builds a Program from per-position input lists; the last list
// is the live-out set.
func sequence(values []Value, inputs ...[]int) Program { return testProgram{values, inputs} }

// ownedInputs is a Program whose first position returns a slice it owns,
// with spare capacity.
type ownedInputs struct{ backing []int }

func (p ownedInputs) Values() int     { return 2 }
func (p ownedInputs) Value(int) Value { return Value{} }
func (p ownedInputs) Positions() int  { return 3 }
func (p ownedInputs) Inputs(position int, dst []int) []int {
	if position == 0 {
		return p.backing[:2]
	}
	return append(dst, 1)
}

func TestResidencyUsesAndNextUse(t *testing.T) {
	var a Residency
	values := make([]Value, 3)
	if err := a.Reset(Config{Registers: gprs(2), SlotLimit: 8}, sequence(values, []int{0}, []int{0, 1}, []int{2}), &recorder{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Uses(0), []int{0, 1}) || a.Remaining(0) != 2 || a.NextUse(1) != 1 || a.NextUse(2) != 2 {
		t.Fatalf("uses %v remaining %d next %d %d", a.Uses(0), a.Remaining(0), a.NextUse(1), a.NextUse(2))
	}
	a.Consume([]int{0})
	if a.NextUse(0) != 1 || a.Remaining(0) != 1 {
		t.Fatal("Consume did not advance the use cursor")
	}
	a.Consume([]int{0, 1})
	if a.NextUse(0) != math.MaxInt {
		t.Fatal("a fully consumed value has a next use")
	}
	if err := a.Reset(Config{Registers: gprs(2)}, sequence(values, []int{3}, nil), &recorder{}); !errors.Is(err, backend.ErrInvalidFunction) {
		t.Fatalf("out-of-range input: %v", err)
	}
}

func TestResidencyEvictsFurthestNextUseAndReloads(t *testing.T) {
	var a Residency
	r := &recorder{}
	// Values 0 and 1 are live in the two registers; 2 needs one. Value 1 is
	// read later than 0, so it is spilled.
	values := []Value{{}, {}, {}}
	if err := a.Reset(Config{Registers: gprs(2), SlotLimit: 4},
		sequence(values, []int{}, []int{0}, []int{2}, []int{1}, nil), r); err != nil {
		t.Fatal(err)
	}
	a.Claim(0, SingleRegister(0))
	a.Claim(1, SingleRegister(1))
	g, err := a.Choose(2, Pins{})
	if err != nil || first(g) != 1 {
		t.Fatalf("Choose = %v, %v", g, err)
	}
	a.Claim(2, g)
	a.Consume([]int{0})
	a.Consume([]int{2})
	if g, err = a.Ensure(1, Pins{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"spill v1 r1 s0", fmt.Sprintf("reload v1 r%d s0", first(g))}
	if !reflect.DeepEqual(r.log, want) {
		t.Fatalf("moves %q, want %q", r.log, want)
	}
	a.Consume([]int{1})
	if slot, _ := a.AllocateSlot(); slot != 0 {
		t.Fatalf("dead value's slot not reused: got %d", slot)
	}
}

func TestResidencyRematerializesConstants(t *testing.T) {
	var a Residency
	r := &recorder{}
	values := []Value{{Constant: true}, {}, {}}
	if err := a.Reset(Config{Registers: gprs(1), SlotLimit: 4},
		sequence(values, []int{0}, []int{1}, []int{0, 2}, nil), r); err != nil {
		t.Fatal(err)
	}
	a.Locations()[1].Incoming[0], a.Locations()[1].IncomingWords = 8, 1
	if _, err := a.Ensure(0, Pins{}); err != nil {
		t.Fatal(err)
	}
	a.Consume([]int{0})
	if _, err := a.Ensure(1, Pins{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Ensure(2, Pins{}); !errors.Is(err, backend.ErrInvalidFunction) {
		t.Fatalf("value without a source: %v", err)
	}
	// The constant is dropped without a spill; the incoming value, which a
	// later choice evicts, is spilled.
	want := []string{"const v0 r0", "incoming v1 r0 @8", "spill v1 r0 s0"}
	if !reflect.DeepEqual(r.log, want) || a.Slots() != 1 {
		t.Fatalf("moves %q slots %d, want %q and one", r.log, a.Slots(), want)
	}
}

func TestResidencyPreferenceBlockedAndBanks(t *testing.T) {
	var a Residency
	values := []Value{{}, {Class: backend.Float}, {}}
	err := a.Reset(Config{Registers: testRegisters(4, 1<<3), SlotLimit: 4,
		Blocked: backend.NewRegisterSet(2)}, sequence(values, nil, []int{0, 1, 2}), &recorder{})
	if err != nil {
		t.Fatal(err)
	}
	a.Prefer(0, 1)
	if g, _ := a.Choose(0, Pins{}); first(g) != 1 {
		t.Fatalf("preference ignored: %v", g)
	}
	if g, _ := a.Choose(1, Pins{}); g.Registers[0] != backend.VectorRegister(3) {
		t.Fatalf("float value outside the vector bank: %v", g)
	}
	a.Prefer(2, 2)
	if g, _ := a.Choose(2, pins(0)); first(g) != 1 {
		t.Fatalf("blocked or pinned register chosen: %v", g)
	}
	a.Block(backend.RegisterSet{})
	if g, _ := a.Choose(2, pins(0)); first(g) != 2 {
		t.Fatalf("unblocked preference ignored: %v", g)
	}
}

func TestResidencyEvictMovesOrSpills(t *testing.T) {
	var a Residency
	r := &recorder{}
	values := []Value{{}, {}}
	if err := a.Reset(Config{Registers: gprs(2), SlotLimit: 4}, sequence(values, nil, []int{0, 1}), r); err != nil {
		t.Fatal(err)
	}
	a.Claim(0, SingleRegister(0))
	if err := a.Evict(0, Pins{}); err != nil || first(a.Group(0)) != 1 || a.Owner(0) != -1 {
		t.Fatalf("Evict did not move: %v, at %d", err, first(a.Group(0)))
	}
	a.Claim(1, SingleRegister(0))
	// With register 0 pinned there is nowhere to move value 0.
	if err := a.Evict(1, pins(0)); err != nil || first(a.Group(0)) != -1 || a.Locations()[0].Slot != 0 {
		t.Fatalf("Evict did not spill: %v", err)
	}
	want := []string{"move v0 r1<-r0", "spill v0 r1 s0"}
	if !reflect.DeepEqual(r.log, want) {
		t.Fatalf("moves %q, want %q", r.log, want)
	}
	if err := a.Evict(2, Pins{}); !errors.Is(err, backend.ErrBackendLimit) {
		t.Fatalf("Evict outside the registers: %v", err)
	}
}

func TestResidencySlots(t *testing.T) {
	var a Residency
	limit := errors.New("limit")
	values := []Value{{Words: 2}, {Class: backend.Vector, StorageWords: 2}, {}}
	if err := a.Reset(Config{Registers: gprs(2), ReservedSlots: 1, SlotLimit: 6, VectorSlotParity: 0,
		LimitError: func(int) error { return limit }}, sequence(values, nil), &recorder{}); err != nil {
		t.Fatal(err)
	}
	if s, _ := a.AllocateValueSlot(0); s != 1 {
		t.Fatalf("pair at %d, want 1 above the reserved slot", s)
	}
	if s, _ := a.AllocateValueSlot(1); s != 4 || a.Slots() != 6 {
		t.Fatalf("vector at %d (slots %d), want even slot 4", s, a.Slots())
	}
	if _, err := a.AllocateSlot(); err != limit {
		t.Fatalf("limit error = %v", err)
	}
	a.FreeSlots(1, 2)
	if s, err := a.AllocateSlots(2); s != 1 || err != nil {
		t.Fatalf("freed pair not reused: %d, %v", s, err)
	}
	a.FreeSlots(1, 1)
	a.FreeSlots(3, 1)
	if _, err := a.AllocateSlots(2); err != limit {
		t.Fatal("non-adjacent free slots formed a pair")
	}
	if base, rest, ok := takeFreePair([]int{7, 2, 5, 3}); !ok || base != 2 || !reflect.DeepEqual(rest, []int{7, 5}) {
		t.Fatalf("takeFreePair = %d %v %v", base, rest, ok)
	}
}

func TestResidencyBuffers(t *testing.T) {
	var a Residency
	values := make([]Value, 10000)
	if err := a.Reset(Config{Registers: gprs(4)}, sequence(values, nil), &recorder{}); err != nil {
		t.Fatal(err)
	}
	a.Finish()
	total, big := a.RetainedBytes(1 << 30)
	if total == 0 || big {
		t.Fatalf("retained %d, big %v", total, big)
	}
	if _, big := a.RetainedBytes(1024); !big {
		t.Fatal("no buffer reported above a small limit")
	}
	freed, largest := a.TrimToBudget(total / 2)
	after, _ := a.RetainedBytes(1 << 30)
	if freed == 0 || after != total-freed || after > total/2 || largest > after {
		t.Fatalf("TrimToBudget freed %d, largest %d: %d -> %d", freed, largest, total, after)
	}
	a.Trim(0)
	if after, _ := a.RetainedBytes(0); after != 0 {
		t.Fatalf("Trim(0) kept %d bytes", after)
	}
	// A large sequence followed by many small ones decays.
	big10k := make([]Value, 10000)
	if err := a.Reset(Config{Registers: gprs(4)}, sequence(big10k, nil), &recorder{}); err != nil {
		t.Fatal(err)
	}
	a.Finish()
	a.Decay()
	for range 64 {
		if err := a.Reset(Config{Registers: gprs(4)}, sequence(make([]Value, 4), nil), &recorder{}); err != nil {
			t.Fatal(err)
		}
		a.Finish()
		a.Decay()
	}
	if after, _ := a.RetainedBytes(0); after > 4096*10 {
		t.Fatalf("decay kept %d bytes", after)
	}
}

// Program.Inputs may return a slice it owns, such as an instruction's
// operand list, or append to dst. Reset must not hand an owned slice back as
// the next dst.
func TestResidencyResetDoesNotWriteInputs(t *testing.T) {
	var a Residency
	backing := []int{0, 1, 7, 7}
	for range 2 {
		if err := a.Reset(Config{Registers: gprs(2)}, ownedInputs{backing}, &recorder{}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(backing, []int{0, 1, 7, 7}) {
		t.Fatalf("Reset wrote into returned inputs: %v", backing)
	}
	if !reflect.DeepEqual(a.Uses(0), []int{0}) || !reflect.DeepEqual(a.Uses(1), []int{0, 1, 2}) {
		t.Fatalf("uses %v %v", a.Uses(0), a.Uses(1))
	}
}

// A vector spill reuses an aligned range freed by an earlier vector, so
// storage follows the peak live demand rather than the number of spills.
func TestResidencyReusesFreedVectorSlots(t *testing.T) {
	for _, words := range []uint8{2, 4} {
		for _, parity := range []int{0, 1} {
			t.Run(fmt.Sprintf("words%d-parity%d", words, parity), func(t *testing.T) {
				var a Residency
				values := []Value{{Class: backend.Vector, StorageWords: words}, {Class: backend.Vector, StorageWords: words}, {Class: backend.Vector, StorageWords: words}}
				// v0 and v1 are live together; v2 is defined after v0's last use.
				config := Config{Registers: testRegisters(1, 1), SlotLimit: parity + 2*int(words), VectorSlotParity: parity}
				if err := a.Reset(config, sequence(values, nil, nil, []int{0}, nil, []int{1}, []int{2}, nil), &recorder{}); err != nil {
					t.Fatal(err)
				}
				defer a.Finish()
				claim := func(id int) {
					t.Helper()
					group, err := a.Choose(id, Pins{})
					if err == nil {
						err = a.Claim(id, group)
					}
					if err != nil {
						t.Fatalf("define v%d: %v", id, err)
					}
				}
				ensure := func(id int) {
					t.Helper()
					if _, err := a.Ensure(id, Pins{}); err != nil {
						t.Fatalf("read v%d: %v (slots %d)", id, err, a.Slots())
					}
				}
				claim(0)
				claim(1)  // spills v0
				ensure(0) // spills v1, reloads v0
				a.Consume([]int{0})
				claim(2) // spills v2 into v0's freed range
				ensure(1)
				a.Consume([]int{1})
				ensure(2)
				a.Consume([]int{2})
				if a.Slots() != parity+2*int(words) {
					t.Fatalf("slots = %d, want %d", a.Slots(), parity+2*int(words))
				}
			})
		}
	}
	for _, test := range []struct {
		free          []int
		words, parity int
		base          int
		rest          []int
	}{
		{[]int{9, 4, 5, 6, 7}, 4, 0, 4, []int{9}},  // freed together
		{[]int{7, 2, 5, 3}, 2, 0, 2, []int{5, 7}},  // found after sorting
		{[]int{3, 4, 5, 6}, 2, 1, 3, []int{5, 6}},  // parity selects the base
		{[]int{1, 2, 4}, 2, 0, -1, []int{1, 2, 4}}, // no aligned range
		{[]int{6, 7}, 4, 0, -1, []int{6, 7}},       // too few slots
	} {
		free := slices.Clone(test.free)
		base, rest, ok := takeFreeRange(free, test.words, test.parity)
		if ok != (test.base >= 0) || ok && base != test.base || !slices.Equal(slices.Sorted(slices.Values(rest)), test.rest) {
			t.Errorf("takeFreeRange(%v, %d, %d) = %d %v %v", test.free, test.words, test.parity, base, rest, ok)
		}
	}
}

// Evicting a value without remaining uses needs no spill storage.
func TestResidencyDoesNotSpillDeadValues(t *testing.T) {
	var a Residency
	mover := &recorder{}
	if err := a.Reset(Config{Registers: gprs(1)}, sequence([]Value{{}, {}}, nil, nil, nil), mover); err != nil {
		t.Fatal(err)
	}
	defer a.Finish()
	group, err := a.Choose(0, Pins{})
	if err == nil {
		err = a.Claim(0, group)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Choose(1, Pins{}); err != nil {
		t.Fatalf("evicting a dead value: %v", err)
	}
	if len(mover.log) != 0 {
		t.Fatalf("dead value moved: %v", mover.log)
	}
}

func TestResidencyEvictsCleanValueBeforeFirstPreservedRegister(t *testing.T) {
	// Registers 0 and 1 are volatile; register 2 costs the client a save.
	config := Config{Registers: gprs(3), SlotLimit: 4, Preserved: backend.NewRegisterSet(2)}
	values := []Value{{}, {}, {}, {}}
	uses := sequence(values, []int{}, []int{0, 1}, []int{2}, []int{3}, []int{0, 1, 2, 3}, nil)

	var a Residency
	r := &recorder{}
	if err := a.Reset(config, uses, r); err != nil {
		t.Fatal(err)
	}
	a.Claim(0, SingleRegister(0))
	a.Claim(1, SingleRegister(1))
	// Value 0 already has a slot, so evicting it needs no spill.
	a.Locations()[0].Slot, a.Locations()[0].SlotWords = 0, 1
	if group, err := a.Choose(2, Pins{}); err != nil || first(group) != 0 || len(r.log) != 0 {
		t.Fatalf("with a clean resident: chose %v (%v), log %v; want register 0 without a spill", group, err, r.log)
	}

	// With every resident needing a spill, the preserved register is taken,
	// and once chosen it is an ordinary free register.
	if err := a.Reset(config, uses, r); err != nil {
		t.Fatal(err)
	}
	a.Claim(0, SingleRegister(0))
	a.Claim(1, SingleRegister(1))
	group, err := a.Choose(2, Pins{})
	if err != nil || first(group) != 2 || len(r.log) != 0 {
		t.Fatalf("with dirty residents: chose %v (%v), log %v; want register 2", group, err, r.log)
	}
	a.Claim(2, group)
	a.Release(2)
	a.Locations()[0].Slot, a.Locations()[0].SlotWords = 0, 1
	if group, err := a.Choose(3, Pins{}); err != nil || first(group) != 2 {
		t.Fatalf("after its first use: chose %v (%v); want the free register 2", group, err)
	}
}

// Registers outside Config.Registers are never owned, preferred or claimed.
func TestResidencyRegistersOutsideConfig(t *testing.T) {
	var a Residency
	if err := a.Reset(Config{Registers: backend.NewRegisterSet(3, backend.VectorRegister(1)), SlotLimit: 4},
		sequence([]Value{{}, {}, {Class: backend.Vector, Constant: true}}, nil, []int{0, 1, 2}), &recorder{}); err != nil {
		t.Fatal(err)
	}
	if err := a.Claim(0, SingleRegister(4)); !errors.Is(err, backend.ErrBackendLimit) {
		t.Fatalf("claim outside the registers: %v", err)
	}
	if err := a.Claim(5, SingleRegister(4)); !errors.Is(err, backend.ErrInvalidFunction) {
		t.Fatalf("claim of an unknown value: %v", err)
	}
	if a.Owner(4) != -1 || a.Evict(4, Pins{}) == nil {
		t.Fatal("a register outside the set is owned or evictable")
	}
	a.Prefer(0, 3)
	a.Prefer(0, 4)
	if _, ok := a.Preference(0); ok {
		t.Fatal("a register outside the set kept a preference")
	}
	a.Prefer(1, 3)
	if r, ok := a.Preference(1); !ok || r != 3 {
		t.Fatalf("preference = %d, %v", r, ok)
	}
	g, err := a.Ensure(2, pins(3))
	if err != nil || g != SingleRegister(backend.VectorRegister(1)) || a.Owner(backend.VectorRegister(1)) != 2 {
		t.Fatalf("Ensure = %+v, %v", g, err)
	}
	if _, err := a.Choose(0, pins(3)); err == nil {
		t.Fatal("a pinned register was chosen")
	}
}
