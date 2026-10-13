package regalloc

// Group is an ordered location for one virtual value. Indices refer
// to the target's allocatable register list, not architectural register
// numbers. A 386 uint64 can use two unrelated 32-bit registers in low/high
// order; current 64-bit targets use one index.
type Group struct {
	Indices [2]int
	Words   uint8
}

func IntegerRegisterWords(bits, registerBits uint8) (uint8, error) {
	if bits == 0 || registerBits != 32 && registerBits != 64 || bits > 128 {
		return 0, ErrBackendLimit
	}
	words := (int(bits) + int(registerBits) - 1) / int(registerBits)
	if words > 2 {
		return 0, ErrBackendLimit
	}
	return uint8(words), nil
}

func SingleRegister(index int) Group {
	return Group{Indices: [2]int{index, -1}, Words: 1}
}

func PairedRegisters(low, high int) (Group, error) {
	if low < 0 || high < 0 || low == high {
		return Group{}, ErrInvalidFunction
	}
	return Group{Indices: [2]int{low, high}, Words: 2}, nil
}

func (group Group) First() int {
	if group.Words == 0 {
		return -1
	}
	return group.Indices[0]
}

func (group Group) Contains(index int) bool {
	for i := 0; i < int(group.Words); i++ {
		if group.Indices[i] == index {
			return true
		}
	}
	return false
}

func SplitIntegerWords(value uint64, registerBits uint8, words int) ([2]uint64, error) {
	var parts [2]uint64
	if words < 1 || words > 2 || registerBits != 32 && registerBits != 64 || registerBits == 64 && words != 1 {
		return parts, ErrBackendLimit
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

// Residency keeps every word of a value assigned or evicted together.
// The group order is significant: its first register holds the low word.
type Residency struct {
	Owners []int
	Groups []Group
}

func NewResidency(registerCount, valueCount int) *Residency {
	state := &Residency{Owners: make([]int, registerCount), Groups: make([]Group, valueCount)}
	for i := range state.Owners {
		state.Owners[i] = -1
	}
	return state
}

func (state *Residency) Group(value int) Group { return state.Groups[value] }
func (state *Residency) First(value int) int   { return state.Groups[value].First() }

func (state *Residency) Claim(value int, group Group) error {
	if value < 0 || value >= len(state.Groups) || state.Groups[value].Words != 0 || group.Words < 1 || group.Words > 2 {
		return ErrInvalidFunction
	}
	for i := 0; i < int(group.Words); i++ {
		index := group.Indices[i]
		if index < 0 || index >= len(state.Owners) || state.Owners[index] != -1 || i == 1 && group.Indices[0] == index {
			return ErrBackendLimit
		}
	}
	state.Groups[value] = group
	for i := 0; i < int(group.Words); i++ {
		state.Owners[group.Indices[i]] = value
	}
	return nil
}

func (state *Residency) Release(value int) Group {
	group := state.Groups[value]
	for i := 0; i < int(group.Words); i++ {
		index := group.Indices[i]
		if state.Owners[index] == value {
			state.Owners[index] = -1
		}
	}
	state.Groups[value] = Group{}
	return group
}

// reserve finds an entire free group, evicting complete values as needed.
// The caller spills a victim in onEvict before its registers are released.
// Pinned indices protect operands of the instruction being scheduled.
func (state *Residency) Reserve(words int, pinned map[int]bool, preferred Group,
	nextUse func(int) int, onEvict func(int, Group) error) (Group, error) {
	if words < 1 || words > 2 || words > len(state.Owners) {
		return Group{}, ErrBackendLimit
	}
	usable := func(group Group) bool {
		if int(group.Words) != words {
			return false
		}
		for i := 0; i < words; i++ {
			index := group.Indices[i]
			if index < 0 || index >= len(state.Owners) || pinned[index] || state.Owners[index] >= 0 || i == 1 && group.Indices[0] == index {
				return false
			}
		}
		return true
	}
	if usable(preferred) {
		return preferred, nil
	}
	for {
		free := Group{Indices: [2]int{-1, -1}, Words: uint8(words)}
		count := 0
		for index, owner := range state.Owners {
			if owner == -1 && !pinned[index] {
				free.Indices[count] = index
				count++
				if count == words {
					return free, nil
				}
			}
		}
		victim, distance := -1, -1
		for value, group := range state.Groups {
			if group.Words == 0 {
				continue
			}
			evictable := true
			for i := 0; i < int(group.Words); i++ {
				evictable = evictable && !pinned[group.Indices[i]]
			}
			if !evictable {
				continue
			}
			next := nextUse(value)
			if victim < 0 || next > distance {
				victim, distance = value, next
			}
		}
		if victim < 0 {
			return Group{}, ErrBackendLimit
		}
		group := state.Groups[victim]
		if err := onEvict(victim, group); err != nil {
			return Group{}, err
		}
		state.Release(victim)
		if usable(preferred) {
			return preferred, nil
		}
	}
}
