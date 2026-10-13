package regalloc

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/backend"
	"math"
	"slices"
	"unsafe"
)

// Value describes one virtual value to a Residency.
type Value struct {
	Class backend.Class
	// Words is the number of registers the value occupies, one or two.
	// Zero means one.
	Words uint8
	// StorageWords is the number of spill-slot words the value needs.
	// Zero means Words.
	StorageWords uint8
	// Constant values are never spilled: Mover.Rematerialize recomputes
	// them.
	Constant bool
}

// Program describes the values and uses of one linear instruction sequence
// to a Residency. Positions are instruction indices; a client with values
// live after the last instruction, such as results and exports, lists them
// as the uses of a final position.
type Program interface {
	Values() int
	Value(value int) Value
	Positions() int
	// Inputs returns the values read at position, appending them to dst or
	// returning a slice it owns; it must not retain dst. A value read twice
	// at one position appears twice.
	Inputs(position int, dst []int) []int
}

// Mover emits the moves a Residency or an Allocator decides on, one method
// per kind of move, destination first.
type Mover interface {
	// Move copies value between register groups of its words. The groups
	// share no register, so the client may copy one word after another.
	Move(value int, to, from Group)
	// Spill stores value into its StorageWords frame slots starting at slot.
	Spill(value int, slot int, from Group)
	// Reload loads value back from its frame slots starting at slot.
	Reload(value int, to Group, slot int)
	// LoadIncoming loads a parameter from the caller's stack words, at
	// offsets from the stack pointer on entry, one per word of to.
	LoadIncoming(value int, to Group, offsets [2]int)
	// Rematerialize recomputes a Constant value into to. It is the only move
	// that can fail: the client may be unable to materialize the constant
	// in that group.
	Rematerialize(value int, to Group) error
}

// Config sets the registers and frame slots for one allocation, by a
// Residency or a Batch.
type Config struct {
	// Registers lists the allocatable registers, at most 64; earlier ones
	// are tried first. Vector and Float values use the vector-file registers
	// (backend.IsVectorRegister), the other classes the rest. The
	// Batch resolves abi register names through the set's names;
	// the Residency does not need them.
	Registers backend.RegisterSet
	// Slots [0, ReservedSlots) belong to the client; the Residency neither
	// hands them out nor reuses them after a value placed there dies.
	ReservedSlots int
	// SlotLimit bounds the frame slots the Residency may use.
	SlotLimit int
	// VectorSlotParity aligns Vector-class spill slots: each starts at a slot
	// index with this parity (0 or 1).
	VectorSlotParity int
	// Blocked lists registers that no choice may use in addition to the
	// caller's pins, for example registers reserved for variable homes.
	// Block changes it during the sequence.
	Blocked backend.RegisterSet
	// Scratch lists Blocked registers that may break cycles in parallel
	// moves, one per bank; without one, a cycle goes through a temporary
	// slot. The Residency does not use it.
	Scratch backend.RegisterSet
	// LimitError returns the error for needing required slots beyond
	// SlotLimit. Nil returns ErrBackendLimit.
	LimitError func(required int) error
	// Preserved lists registers whose first use costs the client a save and
	// a restore, such as the callee-saved registers of a C entry. Until the
	// allocator has taken one, it evicts a value that already has a memory
	// copy, which costs at most a reload, rather than take one. List them
	// last in Registers so that they are tried last.
	Preserved backend.RegisterSet
}

// Residency assigns registers to the values of a linear instruction sequence
// as the client walks it. The client drives it one instruction at a time and
// emits the instruction itself:
//
//	var pins regalloc.Pins
//	group, err := res.Ensure(input, pins) // loads a spilled or new input
//	pins.Add(group)                       // keeps it for this instruction
//	res.Consume(inputs)                   // frees inputs at their last use
//	group, err = res.Choose(result, pins) // picks a register for a result
//	res.Claim(result, group)
//
// When no register is free, the Residency evicts the resident value whose
// next use is furthest away, spilling it through the Mover unless it is a
// Constant or already has a slot. A dead value's registers and slots are
// reused. There is no SSA requirement: a value is defined once, and block
// boundaries are the client's (allocate each block as its own sequence and
// connect them with PlanEdgeTransfer).
//
// The zero value is ready to use. Call Finish after each sequence. A
// Residency keeps its buffers across sequences; RetainedBytes, Trim,
// TrimToBudget and Decay bound what it keeps. It must not be used
// concurrently.
type Residency struct {
	config    Config
	regs      registerIndex
	mover     Mover
	state     []valueState // per value
	occupancy occupancy
	locations []ValueLocation
	useData   []int // every value's uses, in valueState order
	freeSlots []int
	slots     int
	// chosenPreserved holds the indices of Config.Preserved registers
	// already chosen.
	chosenPreserved uint64
	// cleanTies makes an eviction between values of equal next use take
	// one that needs no store; Online sets it.
	cleanTies bool
	inputs    []int // Reset's buffer for Program.Inputs
	history   struct {
		state, locations, useData, freeSlots, owners, held bufferHistory
	}
}

// valueState is what a Residency knows of one value besides its registers
// and location: its description, its preferred register index (or -1), and
// its uses, useData[useStart:useEnd] in position order, of which useCursor
// are consumed and remaining are not.
type valueState struct {
	value                       Value
	preferred                   int32
	remaining                   int32
	useStart, useEnd, useCursor int32
}

// Reset starts allocating program with config, emitting moves through
// mover until Finish. Every value starts without a
// register or location and with no preferred register; set entry state with
// Claim, Locations and Prefer. Reset returns ErrInvalidFunction when an input
// names a value outside the program.
func (a *Residency) Reset(config Config, program Program, mover Mover) error {
	n, positions := program.Values(), program.Positions()
	a.begin(config, n, mover)
	for id := range a.state {
		a.state[id].value = program.Value(id)
	}
	if cap(a.inputs) < 8 {
		a.inputs = make([]int, 0, 8)
	}
	for position := range positions {
		for _, id := range program.Inputs(position, a.inputs[:0]) {
			if id < 0 || id >= n {
				return backend.ErrInvalidFunction
			}
			a.state[id].remaining++
		}
	}
	a.layoutUses()
	for position := range positions {
		for _, id := range program.Inputs(position, a.inputs[:0]) {
			a.addUse(id, position)
		}
	}
	a.rewindUses()
	return nil
}

// begin starts a sequence of n values with config, without their uses.
// The caller sets each state's value, counts its uses in remaining, calls
// layoutUses, adds every use in position order with addUse, and calls
// rewindUses; Reset is that for a Program.
func (a *Residency) begin(config Config, n int, mover Mover) {
	// The previous sequence's registers still index byRegister.
	a.occupancy.clearMirror(!poison && a.occupancy.owners != nil)
	a.config, a.mover = config, mover
	a.regs.reindex(config)
	a.slots = config.ReservedSlots
	a.chosenPreserved, a.cleanTies = 0, false
	a.state = trackedReset(&a.state, &a.history.state, n)
	for id := range a.state {
		a.state[id].preferred = -1
	}
	a.occupancy.owners = trackedReset(&a.occupancy.owners, &a.history.owners, a.regs.count)
	a.occupancy.held = trackedReset(&a.occupancy.held, &a.history.held, n)
	a.occupancy.registers = &a.regs.registers
	a.occupancy.empty()
	a.locations = trackedReset(&a.locations, &a.history.locations, n)
	for id := range a.locations {
		a.locations[id] = ValueLocation{Slot: -1}
	}
	a.freeSlots = a.freeSlots[:0]
	a.history.freeSlots.Observe(0)
}

// layoutUses sizes the use lists from remaining.
func (a *Residency) layoutUses() {
	total := int32(0)
	for id := range a.state {
		x := &a.state[id]
		x.useStart, x.useEnd = total, total+x.remaining
		total = x.useEnd
	}
	a.useData = trackedReset(&a.useData, &a.history.useData, int(total))
}

// addUse records a use of value at position; uses are added in position
// order.
func (a *Residency) addUse(value, position int) {
	x := &a.state[value]
	a.useData[x.useStart+x.useCursor] = position
	x.useCursor++
}

// rewindUses marks every use unconsumed once addUse has added them all.
func (a *Residency) rewindUses() {
	for id := range a.state {
		a.state[id].useCursor = 0
	}
}

// Locations returns every value's spill slot and incoming stack words,
// indexed by value. The client may set entry locations and record spills it
// emits itself. The slice is borrowed until the next Finish, Reset, Trim,
// TrimToBudget or Decay, whichever comes first.
func (a *Residency) Locations() []ValueLocation { return a.locations }

// Uses returns the positions where value is read, in order. The slice is borrowed until the next
// Finish, Reset, Trim, TrimToBudget or Decay, whichever comes first.
func (a *Residency) Uses(value int) []int {
	x := &a.state[value]
	return a.useData[x.useStart:x.useEnd]
}

// Remaining returns how many uses of value have not been consumed.
func (a *Residency) Remaining(value int) int { return int(a.state[value].remaining) }

// NextUse returns the position of value's next unconsumed use, or
// math.MaxInt when it has none.
func (a *Residency) NextUse(value int) int {
	x := &a.state[value]
	if at := x.useStart + x.useCursor; at < x.useEnd {
		return a.useData[at]
	}
	return math.MaxInt
}

// nextUseClean orders values as NextUse does, a value that needs no store to
// be evicted after one at the same position that does.
func (a *Residency) nextUseClean(value int) int {
	next := a.NextUse(value)
	if next == math.MaxInt {
		return next
	}
	next *= 2
	if a.locations[value].Slot >= 0 || a.state[value].value.Constant {
		next++
	}
	return next
}

// Prefer asks for value to be placed in r when it is chosen and r is free.
// A register outside Config.Registers leaves value without a preference.
func (a *Residency) Prefer(value int, r backend.Register) {
	a.state[value].preferred = int32(a.regs.index[r])
}

// Preference returns value's preferred register, or false.
func (a *Residency) Preference(value int) (backend.Register, bool) {
	index := a.state[value].preferred
	return a.regs.registers[index&63], index >= 0
}

// Owner returns the value resident in r, or -1 when r is free or outside
// Config.Registers.
func (a *Residency) Owner(r backend.Register) int { return int(a.occupancy.byRegister[r]) - 1 }

// Group returns value's registers, or an empty group when it is not
// resident.
func (a *Residency) Group(value int) Group { return a.occupancy.held[value].named }

// Claim makes value resident in group. The value must not be resident, and
// the group's registers must be free registers of Config.Registers:
// ErrInvalidFunction reports a bad value or group shape, ErrBackendLimit an
// occupied register or one outside Config.Registers.
func (a *Residency) Claim(value int, group Group) error {
	// A register outside Config.Registers has index -1, which claim rejects
	// after checking the value and the group's shape.
	indices := indexGroup{Indices: [2]int{-1, -1}, Words: group.Words}
	for i := 0; i < int(group.Words) && i < 2; i++ {
		indices.Indices[i] = int(a.regs.index[group.Registers[i]])
	}
	return a.occupancy.claim(value, indices, group)
}

// Release frees value's registers without spilling it and returns them.
func (a *Residency) Release(value int) Group {
	group := a.occupancy.held[value].named
	a.occupancy.release(value)
	return group
}

// Slots returns the number of frame slots in use: the reserved slots plus
// every slot the Residency has handed out.
func (a *Residency) Slots() int { return a.slots }

func (a *Residency) limitError(required int) error {
	if a.config.LimitError != nil {
		return a.config.LimitError(required)
	}
	return backend.ErrBackendLimit
}

func (a *Residency) words(value int) int {
	v := a.state[value].value
	if v.Class == backend.Float || v.Words == 0 {
		return 1
	}
	return int(v.Words)
}

func (a *Residency) storageWords(value int) int {
	if v := a.state[value].value; v.StorageWords != 0 {
		return int(v.StorageWords)
	}
	return a.words(value)
}

// AllocateSlot returns a free slot, reusing a dead value's slot first.
func (a *Residency) AllocateSlot() (int, error) {
	if count := len(a.freeSlots); count != 0 {
		slot := a.freeSlots[count-1]
		a.freeSlots = a.freeSlots[:count-1]
		return slot, nil
	}
	if a.slots >= a.config.SlotLimit {
		return -1, a.limitError(a.slots + 1)
	}
	slot := a.slots
	a.slots++
	return slot, nil
}

// AllocateSlots returns the first of words (one or two) consecutive slots,
// reusing free slots when possible.
func (a *Residency) AllocateSlots(words int) (int, error) {
	if words == 1 {
		return a.AllocateSlot()
	}
	if words != 2 {
		return -1, backend.ErrBackendLimit
	}
	if base, rest, ok := takeFreePair(a.freeSlots); ok {
		a.freeSlots = rest
		return base, nil
	}
	return a.AppendSlots(words)
}

// AppendSlots returns the first of words new consecutive slots above every
// slot in use, without reusing free slots.
func (a *Residency) AppendSlots(words int) (int, error) {
	if a.slots+words > a.config.SlotLimit {
		return -1, a.limitError(a.slots + words)
	}
	base := a.slots
	a.slots += words
	return base, nil
}

// AllocateValueSlot returns slots for value's StorageWords, aligned per
// Config.VectorSlotParity for a Vector value, reusing free slots when
// possible. It does not record them.
func (a *Residency) AllocateValueSlot(value int) (int, error) {
	words := a.storageWords(value)
	if a.state[value].value.Class == backend.Vector {
		if base, rest, ok := takeFreeRange(a.freeSlots, words, a.config.VectorSlotParity); ok {
			a.freeSlots = rest
			return base, nil
		}
		if a.slots%2 != a.config.VectorSlotParity {
			a.slots++
		}
		return a.AppendSlots(words)
	}
	return a.AllocateSlots(words)
}

// FreeSlots returns words slots starting at slot for reuse.
func (a *Residency) FreeSlots(slot, words int) {
	for word := 0; word < words; word++ {
		a.freeSlots = append(a.freeSlots, slot+word)
		a.history.freeSlots.Observe(len(a.freeSlots))
	}
}

// Spill gives value new slots, records them in its location and stores
// group there through the Mover.
func (a *Residency) Spill(value int, group Group) error {
	indices, ok := a.regs.indices(group)
	if !ok {
		return backend.ErrBackendLimit
	}
	return a.spill(value, indices)
}

func (a *Residency) spill(value int, group indexGroup) error {
	slot, err := a.AllocateValueSlot(value)
	if err != nil {
		return err
	}
	a.locations[value].Slot, a.locations[value].SlotWords = slot, uint8(a.storageWords(value))
	a.mover.Spill(value, slot, a.regs.group(group))
	return nil
}

// ChooseGroup finds words free registers of class, avoiding pins and the
// blocked registers. It returns preferred when usable. Otherwise it evicts
// values with the furthest next use, spilling each one that is not a
// Constant, has no slot yet and has uses remaining. While a
// Config.Preserved register has not been chosen yet, a free other register
// or the eviction of a value that needs no spill comes first. ChooseGroup
// does not claim the group.
func (a *Residency) ChooseGroup(words int, class backend.Class, pins Pins, preferred Group) (Group, error) {
	preference, ok := a.regs.indices(preferred)
	if !ok {
		preference = indexGroup{}
	}
	group, err := a.chooseGroup(words, class, a.regs.pins(pins), preference)
	return a.regs.group(group), err
}

func (a *Residency) chooseGroup(words int, class backend.Class, pinned uint64, preferred indexGroup) (indexGroup, error) {
	pinned |= a.regs.blocked
	if class == backend.Vector || class == backend.Float {
		pinned |= ^a.regs.vector
	} else {
		pinned |= a.regs.vector
	}
	if unchosen := a.regs.preserved &^ a.chosenPreserved &^ pinned; unchosen != 0 {
		// A free register other than the unchosen preserved ones is what
		// the attempt below takes first, without the scan.
		if words >= 1 && words <= 2 && words <= len(a.occupancy.owners) && len(a.occupancy.owners) <= 64 {
			if group, ok := a.occupancy.free(words, pinned|unchosen, preferred); ok {
				return group, nil
			}
		}
		// Pin the unchosen preserved registers and every value an eviction
		// would have to spill, so this attempt takes a free register or
		// evicts a value that needs no spill.
		clean := pinned | unchosen
		for index, id := range a.occupancy.owners {
			if id >= 0 && !a.state[id].value.Constant && a.locations[id].Slot < 0 && a.state[id].remaining > 0 {
				clean |= 1 << uint(index)
			}
		}
		if group, err := a.occupancy.reserve(words, clean, preferred, a.NextUse, a.evictValue); err == nil {
			return group, nil
		}
	}
	nextUse := a.NextUse
	if a.cleanTies {
		nextUse = a.nextUseClean
	}
	group, err := a.occupancy.reserve(words, pinned, preferred, nextUse, a.evictValue)
	if err != nil {
		return group, fmt.Errorf("allocate %d-word class %d group: %w", words, class, err)
	}
	for i := range int(group.Words) {
		a.chosenPreserved |= a.regs.preserved & (1 << uint(group.Indices[i]))
	}
	return group, nil
}

// Block replaces Config.Blocked.
func (a *Residency) Block(registers backend.RegisterSet) {
	a.config.Blocked = registers
	a.regs.blocked = a.regs.mask(registers)
}

func (a *Residency) evictValue(id int, group indexGroup) error {
	// A value without remaining uses is dead and needs no copy.
	if x := &a.state[id]; x.value.Constant || a.locations[id].Slot >= 0 || x.remaining == 0 {
		return nil
	}
	return a.spill(id, group)
}

// Choose finds registers for value as ChooseGroup does, trying its
// preferred register first. It does not claim them.
func (a *Residency) Choose(value int, pins Pins) (Group, error) {
	group, err := a.choose(value, a.regs.pins(pins))
	return a.regs.group(group), err
}

func (a *Residency) choose(value int, pinned uint64) (indexGroup, error) {
	preference := indexGroup{}
	if index := a.state[value].preferred; index >= 0 && a.words(value) == 1 {
		preference = singleIndex(int(index))
	}
	return a.chooseGroup(a.words(value), a.state[value].value.Class, pinned, preference)
}

// Ensure returns value's registers, choosing them and loading value through
// the Mover when it is not resident: a Reload from its slot when it has
// one, else a LoadIncoming from its incoming stack words, else (for a
// Constant) a Rematerialize.
func (a *Residency) Ensure(value int, pins Pins) (Group, error) {
	if group := a.occupancy.held[value].group; group.Words != 0 {
		if int(group.Words) != a.words(value) {
			return Group{}, fmt.Errorf("value %d residency words %d != %d: %w", value, group.Words, a.words(value), backend.ErrInvalidFunction)
		}
		return a.occupancy.held[value].named, nil
	}
	if err := a.ensure(value, a.regs.pins(pins)); err != nil {
		return Group{}, err
	}
	return a.occupancy.held[value].named, nil
}

// ensure is Ensure for a value that is not resident, with pins as an index
// mask.
func (a *Residency) ensure(value int, pinned uint64) error {
	group, err := a.choose(value, pinned)
	if err != nil {
		return err
	}
	to := a.regs.group(group)
	location := a.locations[value]
	switch {
	case location.Slot >= 0:
		if int(location.SlotWords) != a.storageWords(value) {
			return fmt.Errorf("value %d spill words %d != %d: %w", value, location.SlotWords, a.storageWords(value), backend.ErrInvalidFunction)
		}
		a.mover.Reload(value, to, location.Slot)
	case location.IncomingWords != 0:
		if int(location.IncomingWords) != a.words(value) {
			return backend.ErrInvalidFunction
		}
		a.mover.LoadIncoming(value, to, location.Incoming)
	case !a.state[value].value.Constant:
		return fmt.Errorf("value %d has no materializable source (class %d): %w", value, a.state[value].value.Class, backend.ErrInvalidFunction)
	default:
		if err := a.mover.Rematerialize(value, to); err != nil {
			return err
		}
	}
	if err := a.occupancy.claim(value, group, to); err != nil {
		return err
	}
	return nil
}

// Consume records one use of each input. A value whose last use this is
// loses its registers, and its slots return to the free list unless they are
// reserved.
func (a *Residency) Consume(inputs []int) {
	for _, id := range inputs {
		a.consume(id)
	}
}

// consume records one use of value.
func (a *Residency) consume(id int) {
	x := &a.state[id]
	x.remaining--
	x.useCursor++
	if x.remaining != 0 {
		return
	}
	a.occupancy.release(id)
	if slot := a.locations[id].Slot; slot >= 0 {
		if slot >= a.config.ReservedSlots {
			a.FreeSlots(slot, int(a.locations[id].SlotWords))
		}
		a.locations[id].Slot, a.locations[id].SlotWords = -1, 0
	}
}

// Evict frees r for a fixed use. A live owner moves to other registers
// (avoiding pins and r) through the Mover when one is free, or is spilled
// when it has no slot; then it is released. Evict returns ErrBackendLimit
// for a register outside Config.Registers.
func (a *Residency) Evict(r backend.Register, pins Pins) error {
	index := int(a.regs.index[r])
	if index < 0 {
		return backend.ErrBackendLimit
	}
	return a.evict(index, a.regs.pins(pins))
}

// evict is Evict for the register at index, with pins as an index mask.
func (a *Residency) evict(index int, pinned uint64) error {
	id := a.occupancy.owners[index]
	if id < 0 {
		return nil
	}
	group := a.occupancy.held[id].group
	if x := &a.state[id]; x.remaining > 0 && !x.value.Constant {
		moved, err := a.choose(id, pinned|1<<uint(index))
		if err == nil {
			to := a.regs.group(moved)
			a.mover.Move(id, to, a.occupancy.held[id].named)
			a.occupancy.release(id)
			return a.occupancy.claim(id, moved, to)
		}
		if a.locations[id].Slot < 0 {
			if err := a.spill(id, group); err != nil {
				return err
			}
		}
	}
	a.occupancy.release(id)
	return nil
}

// RetainedBytes reports the bytes the Residency's buffers retain and whether
// any one buffer exceeds limit.
func (a *Residency) RetainedBytes(limit int) (int, bool) {
	total, big := 0, false
	for _, n := range a.bufferSizes() {
		total += n
		big = big || n > limit
	}
	return total, big
}

func (a *Residency) bufferSizes() [7]int {
	return [7]int{bytesOf(a.inputs), bytesOf(a.state), bytesOf(a.locations), bytesOf(a.useData), bytesOf(a.freeSlots),
		bytesOf(a.occupancy.owners), bytesOf(a.occupancy.held)}
}

// Trim discards every buffer larger than limit bytes.
func (a *Residency) Trim(limit int) {
	trim(&a.inputs, limit)
	trim(&a.state, limit)
	trim(&a.locations, limit)
	trim(&a.useData, limit)
	trim(&a.freeSlots, limit)
	trim(&a.occupancy.owners, limit)
	trim(&a.occupancy.held, limit)
}

// TrimToBudget discards the largest buffers first until the retained storage
// is at most budget bytes. It returns the bytes freed and the largest
// remaining buffer's size, with RetainedBytes's accounting.
func (a *Residency) TrimToBudget(budget int) (freed, largest int) {
	budget = max(0, budget)
	type discard struct {
		bytes int
		drop  func()
	}
	choices := []discard{
		{bytesOf(a.inputs), func() { a.inputs = nil }},
		{bytesOf(a.state), func() { a.state = nil }},
		{bytesOf(a.locations), func() { a.locations = nil }},
		{bytesOf(a.useData), func() { a.useData = nil }},
		{bytesOf(a.freeSlots), func() { a.freeSlots = nil }},
		{bytesOf(a.occupancy.owners), func() { a.occupancy.owners = nil }},
		{bytesOf(a.occupancy.held), func() { a.occupancy.held = nil }},
	}
	total := 0
	for _, c := range choices {
		total += c.bytes
	}
	slices.SortFunc(choices, func(x, y discard) int { return y.bytes - x.bytes })
	for _, c := range choices {
		if c.bytes == 0 {
			break
		}
		if total-freed <= budget {
			return freed, c.bytes
		}
		c.drop()
		freed += c.bytes
	}
	return freed, 0
}

// Decay forgets excess buffer capacity after sustained smaller sequences.
// Call it once after finishing each sequence; a recurring large sequence
// within a few cycles keeps its storage.
func (a *Residency) Decay() {
	decay(&a.state, &a.history.state)
	decay(&a.locations, &a.history.locations)
	decay(&a.useData, &a.history.useData)
	decay(&a.freeSlots, &a.history.freeSlots)
	decay(&a.occupancy.owners, &a.history.owners)
	decay(&a.occupancy.held, &a.history.held)
}

// Finish ends a sequence and drops Config. Results borrowed from the
// Residency expire. A build with nyanjit_scratch_poison overwrites retained
// storage to detect stale borrows.
func (a *Residency) Finish() {
	a.config, a.mover = Config{}, nil
	if !poison {
		return
	}
	for _, buffer := range [][]int{a.useData, a.freeSlots, a.occupancy.owners} {
		buffer = buffer[:cap(buffer)]
		for i := range buffer {
			buffer[i] = -0x5a5a5a
		}
	}
	for i := range a.locations[:cap(a.locations)] {
		a.locations[:cap(a.locations)][i] = ValueLocation{Slot: -0x5a5a}
	}
	for i := range a.occupancy.held[:cap(a.occupancy.held)] {
		a.occupancy.held[:cap(a.occupancy.held)][i] = occupant{group: indexGroup{Words: 255}, named: Group{Words: 255}}
	}
	for i := range a.state[:cap(a.state)] {
		a.state[:cap(a.state)][i] = valueState{value: Value{Class: 255, Words: 255}, preferred: -0x5a5a, remaining: -0x5a5a,
			useStart: -0x5a5a, useEnd: -0x5a5a, useCursor: -0x5a5a}
	}
}

// takeFreePair removes two adjacent slots from free, preferring the two most
// recently freed.
func takeFreePair(free []int) (base int, rest []int, ok bool) {
	n := len(free)
	if n < 2 {
		return 0, free, false
	}
	x, y := free[n-2], free[n-1]
	if x+1 == y || y+1 == x {
		return min(x, y), free[:n-2], true
	}
	seen := make(map[int]int, n)
	for i, slot := range free {
		for _, neighbor := range []int{slot - 1, slot + 1} {
			if j, found := seen[neighbor]; found {
				free = append(free[:i], free[i+1:]...)
				free = append(free[:j], free[j+1:]...)
				return min(slot, neighbor), free, true
			}
		}
		seen[slot] = i
	}
	return 0, free, false
}

// takeFreeRange removes words consecutive free slots starting at a slot of
// the given parity. It first looks for slots freed together, keeping the
// free order, and otherwise sorts free in place to find any such range.
func takeFreeRange(free []int, words, parity int) (base int, rest []int, ok bool) {
	if len(free) < words {
		return 0, free, false
	}
	at := freeRun(free, words, parity)
	if at < 0 {
		slices.Sort(free)
		if at = freeRun(free, words, parity); at < 0 {
			return 0, free, false
		}
	}
	base = free[at]
	return base, slices.Delete(free, at, at+words), true
}

// freeRun returns the index in free of words ascending consecutive slots
// starting at a slot of the given parity, or -1.
func freeRun(free []int, words, parity int) int {
	for i := 0; i+words <= len(free); i++ {
		if free[i]%2 != parity {
			continue
		}
		j := 1
		for j < words && free[i+j] == free[i]+j {
			j++
		}
		if j == words {
			return i
		}
	}
	return -1
}

func bytesOf[T any](s []T) int { var value T; return cap(s) * int(unsafe.Sizeof(value)) }

func trackedReset[T any](s *[]T, h *bufferHistory, n int) []T {
	h.Observe(n)
	if cap(*s) < n {
		*s = make([]T, n)
	} else {
		if len(*s) > n {
			clear((*s)[n:])
		}
		*s = (*s)[:n]
		clear(*s)
	}
	return *s
}

func trim[T any](s *[]T, limit int) {
	if bytesOf(*s) > limit {
		*s = nil
	}
}

func decay[T any](s *[]T, h *bufferHistory) {
	if excess := h.FinishCycle(cap(*s)); bytesOf(*s) > 4096 && excess {
		*s = nil
	}
}
