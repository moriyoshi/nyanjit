package regalloc

// ValueLocation is a virtual value's spill and incoming argument position.
// Incoming offsets are relative to the stack pointer at function entry.
type ValueLocation struct {
	Slot          int
	SlotWords     uint8
	Incoming      [2]int
	IncomingWords uint8
}
