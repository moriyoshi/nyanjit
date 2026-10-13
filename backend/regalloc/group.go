package regalloc

import (
	"math/bits"

	"github.com/moriyoshi/nyanjit/backend"
)

// Group is the registers holding one value, low word first. A 386 uint64
// can use two unrelated 32-bit registers; current 64-bit targets use one.
// Registers past Words are zero and mean nothing, so groups compare equal
// when they hold the same registers; the zero Group holds no value.
type Group struct {
	Registers [2]backend.Register
	Words     uint8
}

// SingleRegister returns a one-word group in r.
func SingleRegister(r backend.Register) Group {
	return Group{Registers: [2]backend.Register{r}, Words: 1}
}

// PairedRegisters returns a two-word group, low word first, or
// ErrInvalidFunction when low and high are the same register.
func PairedRegisters(low, high backend.Register) (Group, error) {
	if low == high {
		return Group{}, backend.ErrInvalidFunction
	}
	return Group{Registers: [2]backend.Register{low, high}, Words: 2}, nil
}

// First returns the register holding the low word, or false for an empty
// group.
func (group Group) First() (backend.Register, bool) {
	return group.Registers[0], group.Words != 0
}

// Contains reports whether the group uses r.
func (group Group) Contains(r backend.Register) bool {
	for i := 0; i < int(group.Words); i++ {
		if group.Registers[i] == r {
			return true
		}
	}
	return false
}

// Pins is a set of registers a choice must neither take nor evict, typically
// the operands already placed for the instruction being emitted. The zero
// value pins nothing.
type Pins struct{ bits [4]uint64 } // one bit per backend.Register

// Add pins group's registers.
func (p *Pins) Add(group Group) {
	for i := 0; i < int(group.Words); i++ {
		p.AddRegister(group.Registers[i])
	}
}

// AddRegister pins r.
func (p *Pins) AddRegister(r backend.Register) { p.bits[r>>6] |= 1 << (r & 63) }

// Remove unpins group's registers.
func (p *Pins) Remove(group Group) {
	for i := 0; i < int(group.Words); i++ {
		r := group.Registers[i]
		p.bits[r>>6] &^= 1 << (r & 63)
	}
}

// Contains reports whether r is pinned.
func (p Pins) Contains(r backend.Register) bool { return p.bits[r>>6]&(1<<(r&63)) != 0 }

// IntegerRegisterWords returns how many registers of registerBits (32 or 64)
// an integer of bits needs: one or two, or ErrBackendLimit.
func IntegerRegisterWords(bits, registerBits uint8) (uint8, error) {
	if bits == 0 || registerBits != 32 && registerBits != 64 || bits > 128 {
		return 0, backend.ErrBackendLimit
	}
	words := (int(bits) + int(registerBits) - 1) / int(registerBits)
	if words > 2 {
		return 0, backend.ErrBackendLimit
	}
	return uint8(words), nil
}

// SplitIntegerWords splits value into words of registerBits, low word first.
// 64-bit registers take one word; 32-bit registers take one or two.
func SplitIntegerWords(value uint64, registerBits uint8, words int) ([2]uint64, error) {
	var parts [2]uint64
	if words < 1 || words > 2 || registerBits != 32 && registerBits != 64 || registerBits == 64 && words != 1 {
		return parts, backend.ErrBackendLimit
	}
	parts[0] = value
	if registerBits == 32 {
		parts[0] &= 0xffffffff
		if words == 2 {
			parts[1] = value >> 32
		}
	}
	return parts, nil
}

// indexGroup is a Group by register index, the position in Config.Registers,
// which is how the allocators track registers internally. Unused indices
// are -1.
type indexGroup struct {
	Indices [2]int
	Words   uint8
}

// singleIndex returns a one-word group at index.
func singleIndex(index int) indexGroup {
	return indexGroup{Indices: [2]int{index, -1}, Words: 1}
}

// First returns the index holding the low word, or -1 for an empty group.
func (group indexGroup) First() int {
	if group.Words == 0 {
		return -1
	}
	return group.Indices[0]
}

// Contains reports whether the group uses index.
func (group indexGroup) Contains(index int) bool {
	for i := 0; i < int(group.Words); i++ {
		if group.Indices[i] == index {
			return true
		}
	}
	return false
}

// occupancy keeps every word of a value assigned or evicted together.
// The group order is significant: its first register holds the low word.
type occupancy struct {
	owners []int
	// freeMask has a bit per index whose owner is -1, for at most 64
	// registers; claim, release and empty keep it.
	freeMask uint64
	held     []occupant // per value
	// When registers names the indices, byRegister and each occupant's named
	// mirror owners and groups by register, so that Residency answers without
	// converting: byRegister holds each register's owner plus one.
	registers  *[64]backend.Register
	byRegister [256]int32
}

// occupant is a value's registers by index and, when the occupancy mirrors
// them, by register.
type occupant struct {
	group indexGroup
	named Group
}

// newOccupancy tracks registerCount registers, all free, and valueCount
// values, none resident.
func newOccupancy(registerCount, valueCount int) *occupancy {
	state := &occupancy{owners: make([]int, registerCount), held: make([]occupant, valueCount)}
	state.empty()
	return state
}

// clearMirror zeroes byRegister. Only registers with owners can be set, so
// exact clears just theirs; without trustworthy owners, it clears all.
func (state *occupancy) clearMirror(exact bool) {
	if !exact || state.registers == nil {
		clear(state.byRegister[:])
		return
	}
	for index, owner := range state.owners {
		if owner >= 0 {
			state.byRegister[state.registers[index]] = 0
		}
	}
}

// empty frees every register.
func (state *occupancy) empty() {
	for i := range state.owners {
		state.owners[i] = -1
	}
	if n := len(state.owners); n >= 64 {
		state.freeMask = ^uint64(0)
	} else {
		state.freeMask = 1<<uint(n) - 1
	}
}

// claim makes value resident in group, whose registers are named, when
// state mirrors them, by named. The value must not be resident and the
// group's registers must be free: ErrInvalidFunction reports a bad value or
// group shape, ErrBackendLimit an occupied or out-of-range register.
func (state *occupancy) claim(value int, group indexGroup, named Group) error {
	if value < 0 || value >= len(state.held) || state.held[value].group.Words != 0 || group.Words < 1 || group.Words > 2 {
		return backend.ErrInvalidFunction
	}
	for i := 0; i < int(group.Words); i++ {
		index := group.Indices[i]
		if index < 0 || index >= len(state.owners) || state.owners[index] != -1 || i == 1 && group.Indices[0] == index {
			return backend.ErrBackendLimit
		}
	}
	state.held[value].group = group
	for i := 0; i < int(group.Words); i++ {
		state.owners[group.Indices[i]] = value
		state.freeMask &^= 1 << uint(group.Indices[i]&63)
	}
	if state.registers != nil {
		for i := 0; i < int(named.Words); i++ {
			state.byRegister[named.Registers[i]] = int32(value) + 1
		}
		state.held[value].named = named
	}
	return nil
}

// release frees value's registers and returns the group it held.
func (state *occupancy) release(value int) indexGroup {
	group := state.held[value].group
	for i := 0; i < int(group.Words); i++ {
		index := group.Indices[i]
		if state.owners[index] == value {
			state.owners[index] = -1
			state.freeMask |= 1 << uint(index&63)
			if state.registers != nil {
				state.byRegister[state.registers[index]] = 0
			}
		}
	}
	state.held[value] = occupant{}
	return group
}

// free returns preferred when its registers are free and unpinned, else the
// first free unpinned registers in index order, without evicting: what
// reserve returns when it need not evict. words must be one or two.
func (state *occupancy) free(words int, pinned uint64, preferred indexGroup) (indexGroup, bool) {
	available := state.freeMask &^ pinned
	usable := int(preferred.Words) == words
	for i := 0; usable && i < words; i++ {
		index := preferred.Indices[i]
		usable = index >= 0 && index < len(state.owners) && available&(1<<uint(index)) != 0 && (i == 0 || preferred.Indices[0] != index)
	}
	if usable {
		return preferred, true
	}
	if bits.OnesCount64(available) < words {
		return indexGroup{}, false
	}
	free := indexGroup{Indices: [2]int{-1, -1}, Words: uint8(words)}
	for i := range words {
		free.Indices[i] = bits.TrailingZeros64(available)
		available &= available - 1
	}
	return free, true
}

// reserve finds a free group of words registers, avoiding pinned ones (a
// bitset of indices, so at most 64 registers). It returns preferred when
// that is usable; otherwise it evicts whole values, furthest next use first
// by nextUse, calling onEvict for each victim before releasing its
// registers. reserve does not claim the group.
func (state *occupancy) reserve(words int, pinned uint64, preferred indexGroup,
	nextUse func(int) int, onEvict func(int, indexGroup) error) (indexGroup, error) {
	if len(state.owners) > 64 || words < 1 || words > 2 || words > len(state.owners) {
		return indexGroup{}, backend.ErrBackendLimit
	}
	isPinned := func(index int) bool { return pinned&(1<<uint(index)) != 0 }
	if group, ok := state.free(words, pinned, preferred); ok {
		return group, nil
	}
	for {
		victim, distance := -1, -1
		for index, value := range state.owners {
			if value < 0 || state.held[value].group.First() != index {
				continue
			}
			group := state.held[value].group
			evictable := true
			for i := 0; i < int(group.Words); i++ {
				evictable = evictable && !isPinned(group.Indices[i])
			}
			if !evictable {
				continue
			}
			next := nextUse(value)
			if victim < 0 || next > distance || next == distance && value < victim {
				victim, distance = value, next
			}
		}
		if victim < 0 {
			return indexGroup{}, backend.ErrBackendLimit
		}
		group := state.held[victim].group
		if err := onEvict(victim, group); err != nil {
			return indexGroup{}, err
		}
		state.release(victim)
		if group, ok := state.free(words, pinned, preferred); ok {
			return group, nil
		}
	}
}
