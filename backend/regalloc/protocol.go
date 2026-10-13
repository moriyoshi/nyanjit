package regalloc

import "github.com/moriyoshi/nyanjit/backend/abi"

// Allocator allocates a Function's registers and emits its moves through a
// Mover while the client walks the function and emits its instructions:
//
//	err := a.Allocate(config, f, m)
//	err = a.Entry()            // places block 0's parameters
//	for each block, for each position p in it:
//		err = a.Before(p)      // moves before p's instruction
//		g := a.Operand(p, i)   // the registers of operand i of p
//		... the client emits p's instruction ...
//		err = a.After(p)       // moves after it, such as stores of definitions
//		at the end of a block, for each of its edges e:
//		err = a.Edge(e)        // the edge's moves, wherever the client places them
//	a.Report(&report)
//	slots := a.Slots()
//	a.Finish()
//
// The client walks blocks in layout order and resolves each block's edges
// right after its last After, before it walks another block, so that an
// allocator can use the registers as they are at the end of the block. An
// edge's moves run on the way from the end of From to the start of To: at
// the end of From when the edge is From's only successor (before its
// branch), right after the branch for a fallthrough, at the start of To when
// the edge is To's only predecessor, or in a block the client adds on the
// edge, which it needs only when EdgeEmpty is false. A client that places
// an edge's moves elsewhere than where it calls Edge collects them from its
// Mover and emits them there.
//
// Batch and Online implement Allocator; Verify checks any implementation.
type Allocator interface {
	// Allocate starts allocating f with config, emitting moves through m
	// until Finish. Abi locations name registers as config.Registers names
	// them. It returns ErrInvalidFunction for a malformed description and
	// ErrBackendLimit (or config.LimitError) for more slots than
	// config.SlotLimit.
	Allocate(config Config, f Function, m Mover) error
	// Entry emits the moves that place block 0's parameters.
	Entry() error
	// Before emits the moves before position's instruction.
	Before(position int) error
	// Operand returns the registers of operand index of position, valid from
	// Before to After: a use's registers before the instruction, a
	// definition's after it. A tied definition's registers hold the tied
	// use's value before it. Operand returns an empty group for a Constant
	// definition, which occupies no register.
	Operand(position, index int) Group
	// Memory returns where a Memory use, operand index of position, is
	// during its instruction: its frame slots (Slot, SlotWords), the caller's
	// stack words (Incoming, IncomingWords), or neither for a Constant, which
	// the client materializes itself.
	Memory(position, index int) ValueLocation
	// After emits the moves after position's instruction.
	After(position int) error
	// Edge emits the moves for one edge, block arguments included.
	Edge(edge int) error
	// EdgeEmpty reports whether an edge needs no moves. It is known once
	// Edge has resolved the edge.
	EdgeEmpty(edge int) bool
	// Slots returns the number of frame slots used, Config.ReservedSlots
	// included, once the walk is complete.
	Slots() int
	// Report sets r.ClobberedRegisters and r.ClobberedFPRegisters to the
	// names of the allocatable registers that any position, call or move
	// writes, with the vector bank's registers in ClobberedFPRegisters, once
	// the walk is complete. It leaves the other fields alone. When
	// Config.Registers is unnamed it sets both to nil.
	Report(r *abi.EmissionReport)
	// Finish ends the function and drops the Mover.
	Finish()
}
