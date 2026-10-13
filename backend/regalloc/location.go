package regalloc

import "github.com/moriyoshi/nyanjit/backend/abi"

// ValueLocation is a virtual value's spill and incoming argument position.
// Incoming offsets are relative to the stack pointer at function entry.
type ValueLocation struct {
	Slot          int
	SlotWords     uint8
	Incoming      [2]int
	IncomingWords uint8
}

// frameSlot returns the first slot of an entry argument the layout places in
// consecutive abi.Frame slots, or -1 when it is elsewhere.
func frameSlot(loc abi.ValueLocation) (int, error) {
	if len(loc.Words) == 0 || loc.Words[0].Storage != abi.Frame {
		return -1, nil
	}
	for k, word := range loc.Words {
		if word.Storage != abi.Frame || word.Slot != loc.Words[0].Slot+k || word.Slot < 0 {
			return -1, invalid("entry location %v", loc)
		}
	}
	return loc.Words[0].Slot, nil
}
