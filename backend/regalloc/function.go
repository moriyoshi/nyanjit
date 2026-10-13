package regalloc

import (
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// Function describes a whole function to a Batch in linear form:
// values defined once, blocks with parameters, and edges carrying arguments.
// No SSA conversion is done; block parameters play the role of phis. A value
// may be used in any block its definition dominates.
//
// Positions are the client's instructions, numbered in its block layout
// order. A position may expand to several machine instructions; moves are
// placed only between positions, so a flag producer and its consumer must
// share one. The methods are called only during Allocate, and the slices
// they return need to stay valid only until the next call. FunctionBuilder
// implements Function from slices.
type Function interface {
	Values() int
	Value(value int) Value
	Positions() int
	// Operands returns the operands of position, appending them to dst or
	// returning a slice it owns; it must not retain dst.
	Operands(position int, dst []Operand) []Operand
	// Blocks returns the number of blocks. Block 0 is the entry, and blocks
	// cover the positions in order: each starts where the previous one ends.
	Blocks() int
	Block(block int) Block
	Edges() int
	Edge(edge int) Edge
	// Call returns the effect of a call at position, or nil. The position's
	// uses, in order, are the arguments, placed at ArgumentLocations (a
	// stack-located argument stays an ordinary use that the client stores);
	// its first definition is the result, placed at ResultLocation.
	// ClobberedRegisters and ClobberedFPRegisters are overwritten; values
	// live across the call prefer the other registers.
	Call(position int) *abi.CallEffect
	// Returns reports a returning position, whose first use is placed at
	// Layout().Result.
	Returns(position int) bool
	// Layout places block 0's parameters (Arguments[i] is parameter i, in
	// registers or incoming stack words) and the returned value.
	Layout() abi.EntryLayout
	// Scopes returns the number of scopes, which reserve registers over runs
	// of positions; see Scope. A function without any returns 0.
	Scopes() int
	Scope(scope int) Scope
	// Clobbers returns the registers position's instruction overwrites
	// besides its definitions, as a call's clobbers but without being a call;
	// it may name registers outside Config.Registers, which are ignored.
	Clobbers(position int) backend.RegisterSet
}

// functionShape is the Function being allocated, with its counts and
// layout read once.
type functionShape struct {
	fn                               Function
	values, positions, blocks, edges int
	layout                           abi.EntryLayout
}

// Block is a non-empty run of positions [Start, End) and the values defined
// on entry to it.
type Block struct {
	Start, End int
	Params     []int
}

// Edge is a control transfer from block From to block To. Args are passed to
// To's Params, in order.
type Edge struct {
	From, To int
	Args     []int
}

// Operand is one value read (a use) or written (a definition) at a position.
type Operand struct {
	Value int
	Def   bool
	// Fixed requires a register group for a constraint abi does not
	// describe, such as a shift count. Words 0 means none.
	Fixed Group
	// Tied makes a definition reuse the register of the use at TiedUse, an
	// index into the position's operands (two-address forms), unless it
	// takes Prefer.
	Tied    bool
	TiedUse int
	// EarlyClobber keeps a definition out of every register the position's
	// uses occupy.
	EarlyClobber bool
	// Memory reads a use from memory instead of registers: the client loads
	// its words itself, so the allocator gives it no registers and keeps a
	// copy where Allocator.Memory reports it. A Memory operand is a use that
	// is neither fixed, tied to, nor placed by abi in registers.
	Memory bool
	// Prefer asks for a definition's registers when they hold no value and
	// none of the position's uses. Words 0 means none. A definition that
	// takes them is not tied; a Fixed or abi placement overrides it, and an
	// allocator may ignore it.
	Prefer Group
}

// Batch is an Allocator that allocates a whole function at once and
// records the moves it needs, which its walk replays through the client's
// Mover. It computes liveness itself, scans blocks in layout order
// carrying register state from an allocated predecessor, evicts the value
// whose next use is furthest away, stores each spilled value once after its
// definition, orders edge moves as parallel moves, and assigns frame slots
// by live range so dead values' slots are reused. An edge to the same or an
// earlier block closes a loop; when a loop needs more registers than a bank
// has, values it never reads stay out of registers on entry to it, so the
// back edge does not reload them.
//
// The zero value is ready to use. It keeps its buffers across functions;
// RetainedBytes, Trim, TrimToBudget and Decay bound what it keeps, and Finish
// ends a function. It must not be used concurrently.
type Batch struct {
	config Config
	regs   registerIndex
	names  [64]string // abi name of each register index, from Config.Registers
	f      functionShape
	mover  Mover
	work   functionWork
	// scopes are the function's reserved registers; baseBlocked is
	// Config.Blocked, which regs.blocked extends with them as the scan goes.
	scopes      scopeMap
	scopeMask   []uint64
	baseBlocked uint64
}

// Allocate allocates f's registers with config, whose fields apply as for
// Residency, and replays the moves through m during the walk. Abi
// locations name registers as config.Registers names them.
//
// Allocate returns ErrInvalidFunction for a malformed description: a value
// defined twice, or used where no definition reaches it; an index out of
// range; edge arguments that don't match the destination's parameters;
// conflicting fixed registers; or an abi location naming a register outside
// the allocatable set. It returns ErrBackendLimit (or config.LimitError) for
// more slots than config.SlotLimit.
func (a *Batch) Allocate(config Config, f Function, m Mover) error {
	if f == nil {
		return invalid("no function")
	}
	a.config, a.mover = config, m
	a.f = functionShape{fn: f, values: f.Values(), positions: f.Positions(), blocks: f.Blocks(), edges: f.Edges(), layout: f.Layout()}
	a.regs.reindex(config)
	for i := range a.regs.count {
		a.names[i] = config.Registers.Name(i)
	}
	return a.allocate()
}

// Operand returns the registers of operand index of position. A fixed
// operand is in its fixed or abi registers, even when a tie also copied it
// elsewhere for a definition.
func (a *Batch) Operand(position, index int) Group {
	w := &a.work
	if position < 0 || position >= len(w.opStart.s)-1 || index < 0 || w.opStart.s[position]+index >= w.opStart.s[position+1] {
		return Group{}
	}
	op := &w.ops.s[w.opStart.s[position]+index]
	if !op.def && op.fixed.Words != 0 {
		return a.regs.group(op.fixed)
	}
	return a.regs.group(op.loc)
}

// Memory returns where a Memory use is: the value's frame slots once it is
// stored, its incoming stack words, or neither for a Constant.
func (a *Batch) Memory(position, index int) ValueLocation {
	w := &a.work
	if position < 0 || position >= len(w.opStart.s)-1 || index < 0 || w.opStart.s[position]+index >= w.opStart.s[position+1] {
		return ValueLocation{Slot: -1}
	}
	v := w.ops.s[w.opStart.s[position]+index].value
	switch {
	case w.spilled.s[v]:
		return ValueLocation{Slot: w.slot.s[v], SlotWords: uint8(a.storageWords(v))}
	case w.incoming.s[v].IncomingWords != 0:
		return w.incoming.s[v]
	}
	return ValueLocation{Slot: -1}
}

// Entry replays the moves that place block 0's parameters, including the
// stores of those that are spilled.
func (a *Batch) Entry() error {
	w := &a.work
	if err := a.replay(w.moves.s[w.entryMoves[0]:w.entryMoves[1]]); err != nil {
		return err
	}
	return a.replay(w.spills.s[w.spillBlock.s[0]:w.spillBlock.s[1]])
}

// Before replays the moves before position: stores of spilled parameters
// when it starts a block other than block 0, then reloads, moves and
// evictions for its operands and calls.
func (a *Batch) Before(position int) error {
	w := &a.work
	if position < 0 || position >= len(w.beforeStart.s)-1 {
		return backend.ErrInvalidFunction
	}
	if b := w.posBlock.s[position]; b != 0 && w.blockStart.s[b] == position {
		if err := a.replay(w.spills.s[w.spillBlock.s[b]:w.spillBlock.s[b+1]]); err != nil {
			return err
		}
	}
	return a.replay(w.moves.s[w.beforeStart.s[position]:w.beforeStart.s[position+1]])
}

// After replays the moves after position: the stores of spilled values it
// defines.
func (a *Batch) After(position int) error {
	w := &a.work
	if position < 0 || position >= len(w.spillAfter.s)-1 {
		return backend.ErrInvalidFunction
	}
	return a.replay(w.spills.s[w.spillAfter.s[position]:w.spillAfter.s[position+1]])
}

// Edge replays the moves for one edge, block arguments included, ordered so
// that no source is overwritten before it is read.
func (a *Batch) Edge(edge int) error {
	w := &a.work
	if edge < 0 || edge >= len(w.edgeMoves.s)-1 {
		return backend.ErrInvalidFunction
	}
	return a.replay(w.moves.s[w.edgeMoves.s[edge]:w.edgeMoves.s[edge+1]])
}

// EdgeEmpty reports whether an edge needs no moves.
func (a *Batch) EdgeEmpty(edge int) bool {
	w := &a.work
	return edge < 0 || edge >= len(w.edgeMoves.s)-1 || w.edgeMoves.s[edge] == w.edgeMoves.s[edge+1]
}

// Slots returns the number of frame slots used, Config.ReservedSlots
// included.
func (a *Batch) Slots() int { return a.work.slots }

// Report sets r.ClobberedRegisters and r.ClobberedFPRegisters to the names
// of the allocatable registers that any position, call or move writes, in
// Config.Registers order, with the vector bank's registers in
// ClobberedFPRegisters. It leaves the other fields alone: frame size and
// stack bytes depend on the client's layout. When Config.Registers is
// unnamed it sets both to nil.
func (a *Batch) Report(r *abi.EmissionReport) {
	r.ClobberedRegisters, r.ClobberedFPRegisters = nil, nil
	if !a.config.Registers.Named() {
		return
	}
	for index := range a.regs.count {
		if a.work.written&(1<<uint(index)) == 0 {
			continue
		}
		name := a.names[index]
		if a.regs.vector&(1<<uint(index)) != 0 {
			r.ClobberedFPRegisters = append(r.ClobberedFPRegisters, name)
		} else {
			r.ClobberedRegisters = append(r.ClobberedRegisters, name)
		}
	}
}

func (a *Batch) replay(moves []recordedMove) error {
	w, m := &a.work, a.mover
	for _, move := range moves {
		switch move.kind {
		case moveCopy:
			m.Move(move.value, a.regs.group(move.to), a.regs.group(move.from))
		case moveSpill:
			slot := w.tempSlot + move.temp
			if move.slotOf >= 0 {
				slot = w.slot.s[move.slotOf]
			}
			m.Spill(move.value, slot, a.regs.group(move.from))
		case moveLoad:
			to := a.regs.group(move.to)
			switch {
			case move.temp >= 0:
				m.Reload(move.value, to, w.tempSlot+move.temp)
			case w.spilled.s[move.value]:
				m.Reload(move.value, to, w.slot.s[move.value])
			case w.incoming.s[move.value].IncomingWords != 0:
				m.LoadIncoming(move.value, to, w.incoming.s[move.value].Incoming)
			default:
				if err := m.Rematerialize(move.value, to); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// RetainedBytes reports the bytes of retained storage and whether any one
// buffer is larger than limit.
func (a *Batch) RetainedBytes(limit int) (int, bool) {
	total, big := 0, false
	for _, b := range a.work.buffers() {
		n := b.bytes()
		total += n
		big = big || n > limit
	}
	return total, big
}

// Trim discards every buffer larger than limit bytes.
func (a *Batch) Trim(limit int) {
	for _, b := range a.work.buffers() {
		if b.bytes() > limit {
			b.drop()
		}
	}
	a.expire()
}

// TrimToBudget discards the largest buffers first until the retained storage
// is at most budget bytes. It returns the bytes freed and the largest
// remaining buffer's size.
func (a *Batch) TrimToBudget(budget int) (freed, largest int) {
	budget = max(0, budget)
	buffers := a.work.buffers()
	total := 0
	for _, b := range buffers {
		total += b.bytes()
	}
	slices.SortFunc(buffers, func(x, y retainedBuffer) int { return y.bytes() - x.bytes() })
	defer a.expire()
	for _, b := range buffers {
		n := b.bytes()
		if n == 0 {
			break
		}
		if total-freed <= budget {
			return freed, n
		}
		b.drop()
		freed += n
	}
	return freed, 0
}

// Decay forgets excess buffer capacity after sustained smaller functions.
// Call it once after finishing each function.
func (a *Batch) Decay() {
	for _, b := range a.work.buffers() {
		b.decay()
	}
	a.expire()
}

// Finish ends a function and drops its Config, Function and Mover; the walk
// methods must not be called again until the next Allocate. A build with
// nyanjit_scratch_poison overwrites retained storage to detect stale use.
func (a *Batch) Finish() {
	a.expire()
	if poison {
		a.work.poison()
	}
}

func (a *Batch) expire() {
	a.config, a.f, a.mover = Config{}, functionShape{}, nil
}
