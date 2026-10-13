package machine

// Barrier names the guarantee an OpBarrier must provide, not an instruction.
// Architectures provide different instructions with different semantics, and
// some provide none for a guarantee; a target reports each through its
// Policy's Barrier method and its encoder rejects any it cannot honour.
// Ordinary memory-ordering fences are not speculation barriers.
type Barrier uint8

const (
	// BarrierSpeculation: no later instruction executes, even speculatively,
	// before every earlier conditional branch has resolved.
	BarrierSpeculation Barrier = iota + 1
	// BarrierMemory: full load/store ordering. Every earlier load and store
	// is ordered before every later load and store, as observed by other
	// threads.
	BarrierMemory
	// BarrierInstruction: later instruction fetches observe code stores that
	// were made visible to this thread before the barrier. Making a store
	// visible to fetch may also need the writer's cache maintenance, which
	// this barrier does not perform.
	BarrierInstruction
)

// Valid reports whether b names a known guarantee.
func (b Barrier) Valid() bool { return b >= BarrierSpeculation && b <= BarrierInstruction }

// BarrierContract says what a target's barrier for a guarantee rests on.
type BarrierContract uint8

const (
	// BarrierUnavailable: the target has no barrier with this guarantee.
	BarrierUnavailable BarrierContract = iota
	// BarrierArchitectural: the instruction set defines the guarantee.
	BarrierArchitectural
	// BarrierPlatform: the guarantee also needs processor, firmware or
	// operating-system configuration that the code cannot check, such as
	// dispatch-serializing LFENCE or the POWER ori 31,31,0 barrier.
	BarrierPlatform
)
