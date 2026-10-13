package regalloc

import (
	"fmt"
	"math/bits"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// Online is an Allocator that allocates as the client walks, for speed.
// Within a block a Residency keeps values in registers, evicting the value
// used furthest away. Values flow between blocks through block parameters:
// the first edge resolved into a block fixes where its parameters start
// (in the registers their arguments already occupy when they are free, in
// other free registers otherwise, or in frame slots), and every edge is a
// parallel move from the registers and slots at the end of its source into
// that entry state. A value used in a block other than its own without being
// passed as an argument has a home slot instead: it is stored there once
// after its definition, and reloaded where it is used. Online does no
// liveness analysis, so clients that pass cross-block values explicitly as
// block arguments get the best code from it.
//
// Online checks a description as Batch does, except that it does not check
// that every use is dominated by a definition. The zero value is ready to
// use; Finish ends a function. It must not be used concurrently.
type Online struct {
	res    Residency
	config Config
	f      functionShape
	mover  Mover
	named  map[string]backend.Register
	// fused is set while read lays out the uses itself, from a
	// FunctionBuilder's counts; late when a use came before its
	// definition, so assignHomes lays them out again.
	fused, late bool

	ops                []Operand
	opStart, posBlock  []int
	blockStart         []int // per block, then the end of the last
	paramStart, params []int
	edgeFrom, edgeTo   []int
	argStart, args     []int
	defBlock, defPos   []int // defPos is -1 for a block parameter
	home               []int // per value, or -1
	targets            []Group
	groups             []Group         // per operand, from Before
	memory             []ValueLocation // per operand: where a Memory use is
	hasMemory          bool            // whether memory is sized: there are Memory uses
	memoryInputs       []int           // this position's Memory uses, consumed after it
	callIndex          []int           // per position, into calls, or -1
	calls              []onlineCall
	clobberBuf         []backend.Register // backs the calls' clobbers
	result             Group
	edgeEmpty          []bool
	inputs             []int
	placing            []int // before's buffer: the fixed uses not yet placed
	written            Pins
	opsBuf             []Operand // ops, unless a FunctionBuilder's
	opStartBuf         []int     // opStart, likewise
	paramsBuf, argsBuf []int     // params and args, likewise
	intBuf             []int     // backs the int tables
	boolBuf            []bool    // backs the bool tables
	// Each block's entry state, once fixed: where its parameters start.
	entryKnown         []bool
	paramLocs          []paramLoc // aligned with params
	paramSlot          []int      // per value: the slot a parameter starts in, or -1
	outStart, outEdges []int      // each block's edges, in edge order
	predStart, preds   []int      // each block's incoming edges
	edgeDone           []bool
	pending            []edgeMove // Edge's parallel move
	blockers           []int      // Edge's buffer: per pending move, the moves reading its destination
	temps              []int      // Edge's buffer: cycle slots and their words
	edge               edgeState  // Edge's indexes of the pending moves
	edgeAvoid          Pins       // the registers Edge's scratch registers avoid
	pool               []poolSlot // slots parameters start in, shared by every block
	resolving          int        // the edge Edge is resolving
	namedFor           backend.RegisterSet
	simple             []bool // per position: no fixed or early-clobber operand, memory use, call or clobber
	writtenIndex       uint64 // indices of registers simple positions write
	scopes             scopeMap
	blockedIn          []uint64 // per scope: Config.Blocked and its registers, by index
	blockedBase        uint64   // Config.Blocked by index
	reservedIn         []uint64 // per scope, its registers by index
	scope              int      // the scope whose registers the Residency blocks, or -1
}

// onlineCall is a call's registers: those it overwrites.
type onlineCall struct{ clobbers []backend.Register }

// edgeLoc is where a value is around an edge: registers, a frame slot, the
// caller's stack words, or its constant.
type edgeLoc struct {
	group    Group
	incoming bool // the caller's stack words, which the value's location holds
	constant bool
	slot     int // >= 0 for a frame slot
}

// paramLoc is where a parameter starts: registers, or a frame slot.
type paramLoc struct {
	group Group
	slot  int // >= 0 for a frame slot
}

func (l paramLoc) edge() edgeLoc { return edgeLoc{group: l.group, slot: l.slot} }

// poolSlot is a slot parameters start in. A parameter is dead outside its
// block (a value used elsewhere has a home), and only one edge's moves run
// at a time, so parameters of different blocks may share one.
type poolSlot struct {
	slot, words int
	block       int // 1 + the last block to start a parameter in it
	vector      bool
}

// edgeMove is one move of an edge's parallel move.
type edgeMove struct {
	arg      int
	to, from edgeLoc
	done     bool // made, by Edge's parallel move
}

// value returns value's description, which the Residency holds.
func (o *Online) value(value int) Value { return o.res.state[value].value }

func (o *Online) words(value int) int {
	v := o.value(value)
	if v.Class == backend.Float || v.Words == 0 {
		return 1
	}
	return int(v.Words)
}

func (o *Online) storage(value int) int {
	if v := o.value(value); v.StorageWords != 0 {
		return int(v.StorageWords)
	}
	return o.words(value)
}

func (o *Online) vector(value int) bool {
	c := o.value(value).Class
	return c == backend.Vector || c == backend.Float
}

func (o *Online) limitError(required int) error {
	if o.config.LimitError != nil {
		return o.config.LimitError(required)
	}
	return backend.ErrBackendLimit
}

// resolve returns the registers an abi location names, or false when it is
// on the stack. It returns ErrInvalidFunction for a register outside
// Config.Registers.
func (o *Online) resolve(loc abi.ValueLocation) (Group, bool, error) {
	for _, word := range loc.Words {
		if word.Storage == abi.Frame {
			return Group{}, false, invalid("frame location %v outside the entry arguments", loc)
		}
	}
	if len(loc.Words) == 0 || loc.Words[0].Storage != abi.Register {
		return Group{}, false, nil
	}
	if len(loc.Words) > 2 {
		return Group{}, false, invalid("location %v", loc)
	}
	g := Group{Words: uint8(len(loc.Words))}
	for i, word := range loc.Words {
		r, ok := o.register(word.Register)
		if word.Storage != abi.Register || !ok {
			return Group{}, false, invalid("location %v names a register outside the allocatable set", loc)
		}
		g.Registers[i] = r
	}
	if g.Words == 2 && g.Registers[0] == g.Registers[1] {
		return Group{}, false, invalid("location %v", loc)
	}
	return g, true, nil
}

// register returns the register of Config.Registers named name, from the
// names' map once a call's clobbers have built it, by a scan before.
func (o *Online) register(name string) (backend.Register, bool) {
	if o.named != nil && o.config.Registers == o.namedFor {
		r, ok := o.named[name]
		return r, ok
	}
	for r := range o.config.Registers.All() {
		if o.config.Registers.NameOf(r) == name {
			return r, true
		}
	}
	return 0, false
}

// mapNames maps Config.Registers' names, kept while the set stays the same.
func (o *Online) mapNames() {
	if o.named != nil && o.config.Registers == o.namedFor {
		return
	}
	if o.named == nil {
		o.named = map[string]backend.Register{}
	}
	clear(o.named)
	for r := range o.config.Registers.All() {
		if name := o.config.Registers.NameOf(r); name != "" {
			o.named[name] = r
		}
	}
	o.namedFor = o.config.Registers
}

// Allocate reads f, gives every value that crosses a block boundary a home
// slot and starts its Residency.
func (o *Online) Allocate(config Config, f Function, m Mover) error {
	if f == nil {
		return invalid("no function")
	}
	o.config, o.mover = config, m
	if b, ok := f.(*FunctionBuilder); ok {
		o.f = functionShape{fn: f, values: len(b.values), positions: len(b.positions), blocks: len(b.blocks), edges: len(b.edges), layout: b.layout}
	} else {
		o.f = functionShape{fn: f, values: f.Values(), positions: f.Positions(), blocks: f.Blocks(), edges: f.Edges(), layout: f.Layout()}
	}
	o.written, o.writtenIndex = Pins{}, 0
	o.memoryInputs = o.memoryInputs[:0] // left by an abandoned walk
	// The Residency counts uses as read finds them; its reserved slots
	// are known once the homes are.
	o.res.begin(config, o.f.values, m)
	o.res.cleanTies = true
	if err := o.read(); err != nil {
		return err
	}
	if err := readScopes(f, o.f.positions, config.Registers, &o.scopes); err != nil {
		return err
	}
	o.blockedBase = o.res.regs.blocked
	o.blockedIn, o.reservedIn = o.blockedIn[:0], o.reservedIn[:0]
	for _, set := range o.scopes.sets {
		m := o.res.regs.pins(set)
		o.blockedIn, o.reservedIn = append(o.blockedIn, m|o.blockedBase), append(o.reservedIn, m)
	}
	o.scope = -1
	reserved, err := o.assignHomes()
	if err != nil {
		return err
	}
	o.res.config.ReservedSlots, o.res.slots = reserved, reserved
	locations := o.res.Locations()
	for v, h := range o.home {
		if h >= 0 {
			locations[v].Slot, locations[v].SlotWords = h, uint8(o.storage(v))
		}
	}
	params := o.params[o.paramStart[0]:o.paramStart[1]]
	for i, p := range params {
		loc := o.f.layout.Arguments[i]
		if slot, _ := frameSlot(loc); slot >= 0 {
			// It starts in a slot the client reserved, which stays its own.
			if len(loc.Words) != o.storage(p) || slot+len(loc.Words) > config.ReservedSlots {
				return invalid("entry argument %d location %v", i, loc)
			}
			locations[p].Slot, locations[p].SlotWords = slot, uint8(len(loc.Words))
			continue
		}
		if len(loc.Words) != 0 && loc.Words[0].Storage == abi.Stack {
			if len(loc.Words) != o.words(p) || len(loc.Words) > 2 {
				return invalid("entry argument %d location %v", i, loc)
			}
			for k, word := range loc.Words {
				if word.Storage != abi.Stack {
					return invalid("entry argument %d location %v", i, loc)
				}
				locations[p].Incoming[k] = word.StackOffset
			}
			locations[p].IncomingWords = uint8(len(loc.Words))
			// Entry stores it to its home first.
			locations[p].Slot, locations[p].SlotWords = -1, 0
		}
	}
	return nil
}

// read copies and checks the description.
func (o *Online) read() error {
	f := &o.f
	if f.values < 0 || f.positions < 1 || f.blocks < 1 || f.edges < 0 {
		return invalid("incomplete description")
	}
	// A FunctionBuilder is read from its slices, and its operands,
	// parameters and arguments in place, which it keeps unchanged until
	// Finish; others are copied.
	builder, _ := f.fn.(*FunctionBuilder)
	if builder != nil && (len(builder.values) != f.values || len(builder.positions) != f.positions || len(builder.opStart) != f.positions+1 ||
		len(builder.blocks) != f.blocks || len(builder.edges) != f.edges) {
		builder = nil
	}
	// The values are the Residency's, which begin sized. A builder has
	// counted their uses, so the use lists can be laid out now and filled
	// as the positions are read.
	o.fused = builder != nil && len(builder.uses) <= f.values
	o.late = false
	total := int32(0)
	for v := range o.res.state {
		x := &o.res.state[v].value
		if builder != nil {
			*x = builder.values[v]
		} else {
			*x = f.fn.Value(v)
		}
		if x.Class != backend.Float && x.Words > 2 {
			return fmt.Errorf("value %d has %d words: %w", v, x.Words, backend.ErrBackendLimit)
		}
		if o.fused && v < len(builder.uses) {
			x := &o.res.state[v]
			x.useStart, x.useEnd = total, total+builder.uses[v]
			total = x.useEnd
		}
	}
	if o.fused {
		for v := len(builder.uses); v < len(o.res.state); v++ {
			o.res.state[v].useStart, o.res.state[v].useEnd = total, total
		}
		o.res.useData = trackedReset(&o.res.useData, &o.res.history.useData, int(total))
	}
	// The per-function tables share two buffers.
	o.intBuf = resize(o.intBuf, 4*f.values+4*(f.blocks+1)+2*f.positions+5*f.edges+1)
	ints := o.intBuf
	take := func(n int) []int {
		s := ints[:n:n]
		ints = ints[n:]
		return s
	}
	o.defBlock, o.defPos, o.home, o.paramSlot = take(f.values), take(f.values), take(f.values), take(f.values)
	o.blockStart, o.paramStart = take(f.blocks+1), take(f.blocks+1)
	o.outStart, o.predStart = take(f.blocks+1), take(f.blocks+1)
	o.posBlock, o.callIndex = take(f.positions), take(f.positions)
	o.edgeFrom, o.edgeTo, o.outEdges, o.preds = take(f.edges), take(f.edges), take(f.edges), take(f.edges)
	o.argStart = take(f.edges + 1)
	o.boolBuf = resize(o.boolBuf, f.positions+2*f.edges+f.blocks)
	bools := o.boolBuf
	o.simple, bools = bools[:f.positions:f.positions], bools[f.positions:]
	o.edgeEmpty, bools = bools[:f.edges:f.edges], bools[f.edges:]
	o.edgeDone, o.entryKnown = bools[:f.edges:f.edges], bools[f.edges:]
	for v := range o.defBlock {
		o.defBlock[v], o.home[v] = -1, -1
	}
	define := func(v, b, p int) error {
		if o.defBlock[v] >= 0 {
			return invalid("value %d defined twice", v)
		}
		o.defBlock[v], o.defPos[v] = b, p
		return nil
	}
	if builder != nil {
		o.params = builder.params
	} else {
		o.params = o.paramsBuf[:0]
	}
	params := 0
	next := 0
	for b := range f.blocks {
		var block Block
		if builder != nil {
			block = builder.Block(b) // direct, to inline
		} else {
			block = f.fn.Block(b)
		}
		if block.Start != next || block.End <= block.Start || block.End > f.positions {
			return invalid("block %d covers [%d, %d) after %d", b, block.Start, block.End, next)
		}
		o.blockStart[b], o.paramStart[b] = block.Start, params
		if builder != nil && builder.blocks[b].from != params {
			return invalid("block %d parameters out of order", b)
		}
		params += len(block.Params)
		for _, v := range block.Params {
			if v < 0 || v >= f.values {
				return invalid("block %d parameter %d", b, v)
			}
			if err := define(v, b, -1); err != nil {
				return err
			}
			if builder == nil {
				o.params = append(o.params, v)
			}
		}
		for p := block.Start; p < block.End; p++ {
			o.posBlock[p] = b
		}
		next = block.End
	}
	if next != f.positions {
		return invalid("blocks end at %d of %d positions", next, f.positions)
	}
	if builder == nil {
		o.paramsBuf = o.params
	}
	o.params = o.params[:params]
	o.blockStart[f.blocks], o.paramStart[f.blocks] = f.positions, params
	if n := o.paramStart[1]; n != len(f.layout.Arguments) {
		return invalid("block 0 has %d parameters for %d entry arguments", n, len(f.layout.Arguments))
	}
	for _, loc := range f.layout.Arguments {
		if slot, err := frameSlot(loc); err != nil {
			return err
		} else if slot >= 0 {
			continue // checked against the reserved slots in Allocate
		}
		if _, _, err := o.resolve(loc); err != nil {
			return err
		}
	}
	var err error
	if o.result, _, err = o.resolve(f.layout.Result); err != nil {
		return err
	}
	// Edges first, so that a fused walk below can add their arguments as uses
	// at the end of each block.
	if builder != nil {
		o.args = builder.args
	} else {
		o.args = o.argsBuf[:0]
	}
	args := 0
	for e := range f.edges {
		var edge Edge
		if builder != nil {
			edge = builder.Edge(e)
		} else {
			edge = f.fn.Edge(e)
		}
		if edge.From < 0 || edge.From >= f.blocks || edge.To <= 0 || edge.To >= f.blocks {
			return invalid("edge %d from %d to %d", e, edge.From, edge.To)
		}
		if len(edge.Args) != o.paramStart[edge.To+1]-o.paramStart[edge.To] {
			return invalid("edge %d passes %d arguments to %d parameters", e, len(edge.Args), o.paramStart[edge.To+1]-o.paramStart[edge.To])
		}
		o.edgeFrom[e], o.edgeTo[e], o.argStart[e] = edge.From, edge.To, args
		if builder != nil && builder.edges[e].argFrom != args {
			return invalid("edge %d arguments out of order", e)
		}
		args += len(edge.Args)
		for i, a := range edge.Args {
			if a < 0 || a >= f.values {
				return invalid("edge %d argument %d", e, a)
			}
			if !o.fused {
				o.res.state[a].remaining++ // read at the end of the source
			}
			p := o.params[o.paramStart[edge.To]+i]
			if o.words(a) != o.words(p) || o.vector(a) != o.vector(p) {
				return invalid("edge %d passes value %d to parameter %d of another shape", e, a, p)
			}
			if builder == nil {
				o.args = append(o.args, a)
			}
		}
	}
	if builder == nil {
		o.argsBuf = o.args
	}
	o.args = o.args[:args]
	o.argStart[f.edges] = args
	for e := range f.edges {
		o.outStart[o.edgeFrom[e]+1]++
		o.predStart[o.edgeTo[e]+1]++
	}
	for b := range f.blocks {
		o.outStart[b+1] += o.outStart[b]
		o.predStart[b+1] += o.predStart[b]
	}
	// Fill each block's lists from its end: outStart[b+1] and
	// predStart[b+1] count down to the starts.
	for e := f.edges - 1; e >= 0; e-- {
		from, to := o.edgeFrom[e], o.edgeTo[e]
		o.outStart[from+1]--
		o.outEdges[o.outStart[from+1]] = e
		o.predStart[to+1]--
		o.preds[o.predStart[to+1]] = e
	}
	for b := range f.blocks {
		o.outStart[b], o.predStart[b] = o.outStart[b+1], o.predStart[b+1]
	}
	o.outStart[f.blocks], o.predStart[f.blocks] = f.edges, f.edges
	if builder != nil {
		o.ops, o.opStart = builder.ops, builder.opStart
		o.targets = resize(o.targets, len(o.ops))
	} else {
		o.targets = o.targets[:0]
		o.ops, o.opStart = o.opsBuf[:0], resize(o.opStartBuf, f.positions+1)
		o.opStartBuf = o.opStart
	}
	o.calls, o.clobberBuf = o.calls[:0], o.clobberBuf[:0]
	memory := false
	for p := range f.positions {
		var ops []Operand
		if builder != nil {
			ops = o.ops[o.opStart[p]:o.opStart[p+1]]
		} else {
			// Operands may append to our buffer or return a slice of its own.
			n := len(o.ops)
			o.opStart[p] = n
			if list := f.fn.Operands(p, o.ops[n:n]); len(list) != 0 && cap(o.ops) > n && &o.ops[:n+1][n] == &list[0] {
				o.ops = o.ops[:n+len(list)]
			} else {
				o.ops = append(o.ops, list...)
			}
			o.opsBuf = o.ops
			ops = o.ops[n:]
			// Copied operands grow targets, zeroed, alongside.
			for len(o.targets) < len(o.ops) {
				o.targets = append(o.targets, Group{})
			}
		}
		n := o.opStart[p]
		var effect *abi.CallEffect
		var returns, clobbers bool
		if builder != nil {
			effect = builder.Call(p)
			x := &builder.positions[p]
			returns, clobbers = x.returns, x.clobber != 0
		} else {
			effect, returns, clobbers = f.fn.Call(p), f.fn.Returns(p), true
		}
		simple := effect == nil && !returns
		use, def, targets := 0, 0, 0
		for i := range ops {
			x := &ops[i]
			if x.Value < 0 || x.Value >= f.values {
				return invalid("position %d operand value %d", p, x.Value)
			}
			if !x.Def {
				if o.fused {
					o.fuseUse(x.Value, p)
				} else {
					o.res.state[x.Value].remaining++
				}
			}
			if x.Tied && (!x.Def || x.TiedUse < 0 || x.TiedUse >= len(ops) || ops[x.TiedUse].Def) {
				return invalid("position %d tie to operand %d", p, x.TiedUse)
			}
			if x.Memory && (x.Def || x.Fixed.Words != 0) || x.Tied && ops[x.TiedUse].Memory {
				return invalid("position %d reads value %d from memory with a register constraint", p, x.Value)
			}
			if x.Prefer.Words != 0 {
				if err := checkPrefer(&o.res.regs, p, x, o.words(x.Value), o.value(x.Value).Class); err != nil {
					return err
				}
			}
			target := Group{}
			if x.Fixed.Words != 0 {
				if x.Fixed.Words > 2 || x.Fixed.Words == 2 && x.Fixed.Registers[0] == x.Fixed.Registers[1] || int(x.Fixed.Words) != o.words(x.Value) {
					return invalid("position %d fixed group %v", p, x.Fixed)
				}
				for w := range int(x.Fixed.Words) {
					if !o.config.Registers.Contains(x.Fixed.Registers[w]) {
						return invalid("position %d fixed group %v", p, x.Fixed)
					}
				}
				target = x.Fixed
			}
			if x.Def {
				if err := define(x.Value, o.posBlock[p], p); err != nil {
					return err
				}
				if effect != nil && def == 0 {
					if g, ok, err := o.resolve(effect.ResultLocation); err != nil {
						return err
					} else if ok {
						target = g
					}
				}
				def++
			} else {
				if effect != nil && use < len(effect.ArgumentLocations) {
					if g, ok, err := o.resolve(effect.ArgumentLocations[use]); err != nil {
						return err
					} else if ok {
						target = g
					}
				}
				if returns && use == 0 {
					if o.result.Words == 0 {
						return invalid("result location %v", f.layout.Result)
					}
					target = o.result
				}
				use++
			}
			if target.Words != 0 {
				if x.Memory {
					return invalid("position %d reads value %d from memory at an abi register", p, x.Value)
				}
				if int(target.Words) != o.words(x.Value) {
					return invalid("position %d operand %d in %v", p, x.Value, target)
				}
				targets++
			}
			simple = simple && target.Words == 0 && !x.Memory && !x.EarlyClobber
			memory = memory || x.Memory
			if target.Words != 0 {
				o.targets[n+i] = target
			}
		}
		if targets > 1 {
			for i := range ops {
				x, ti := &ops[i], o.targets[n+i]
				if ti.Words == 0 {
					continue
				}
				for j, y := range ops[:i] {
					if x.Def == y.Def && (x.Def || x.Value != y.Value) && groupsOverlap(ti, o.targets[n+j]) {
						return invalid("position %d needs values %d and %d in one register", p, y.Value, x.Value)
					}
				}
			}
		}
		o.callIndex[p] = -1
		var call onlineCall
		if effect != nil {
			if use < len(effect.ArgumentLocations) {
				return invalid("call at %d has fewer uses than arguments", p)
			}
			if len(effect.ClobberedRegisters)+len(effect.ClobberedFPRegisters) != 0 {
				o.mapNames()
			}
			for _, names := range [][]string{effect.ClobberedRegisters, effect.ClobberedFPRegisters} {
				for _, name := range names {
					if r, ok := o.register(name); ok {
						call.clobbers = append(call.clobbers, r)
					}
				}
			}
		}
		if clobbers {
			// Every call's clobbers share one buffer.
			start := len(o.clobberBuf)
			for r := range f.fn.Clobbers(p).All() {
				if o.res.regs.index[r] >= 0 {
					o.clobberBuf = append(o.clobberBuf, r)
				}
			}
			if listed := o.clobberBuf[start:len(o.clobberBuf):len(o.clobberBuf)]; len(call.clobbers) == 0 {
				call.clobbers = listed
			} else {
				call.clobbers = append(call.clobbers, listed...)
			}
		}
		if effect != nil || len(call.clobbers) != 0 {
			o.callIndex[p] = len(o.calls)
			o.calls = append(o.calls, call)
			simple = false
		}
		o.simple[p] = simple
		if b := o.posBlock[p]; o.fused && o.blockStart[b+1]-1 == p {
			for _, e := range o.outEdges[o.outStart[b]:o.outStart[b+1]] {
				for _, a := range o.args[o.argStart[e]:o.argStart[e+1]] {
					o.fuseUse(a, p)
				}
			}
		}
	}
	if builder == nil {
		o.opStart[f.positions] = len(o.ops)
	}
	o.groups = resize(o.groups, len(o.ops))
	if memory {
		o.memory = resize(o.memory, len(o.ops))
	}
	o.hasMemory = memory
	return nil
}

// fuseUse adds a use of v at position p, as assignHomes does, while read
// counts it.
func (o *Online) fuseUse(v, p int) {
	x := &o.res.state[v]
	o.res.useData[x.useStart+x.remaining] = p
	x.remaining++
	if d := o.defBlock[v]; d != o.posBlock[p] {
		if d < 0 {
			o.late = true // or never defined: assignHomes says which
		} else if !x.value.Constant {
			o.home[v] = -2
		}
	}
}

// addUses adds every use to the Residency, edge arguments as uses at the
// end of their source, and marks the values used outside their definition's
// block with -2 in home.
func (o *Online) addUses() error {
	cross, res := o.home, &o.res
	for v := range cross {
		cross[v] = -1
	}
	res.layoutUses()
	for p, b := range o.posBlock {
		for i := o.opStart[p]; i < o.opStart[p+1]; i++ {
			if x := &o.ops[i]; !x.Def {
				res.addUse(x.Value, p)
				if d := o.defBlock[x.Value]; d != b {
					if d < 0 {
						return invalid("value %d used but never defined", x.Value)
					}
					if !o.value(x.Value).Constant {
						cross[x.Value] = -2
					}
				}
			}
		}
		if o.blockStart[b+1]-1 == p {
			for _, e := range o.outEdges[o.outStart[b]:o.outStart[b+1]] {
				for _, a := range o.args[o.argStart[e]:o.argStart[e+1]] {
					res.addUse(a, p)
					if d := o.defBlock[a]; d != b {
						if d < 0 {
							return invalid("value %d passed but never defined", a)
						}
						if !o.value(a).Constant {
							cross[a] = -2
						}
					}
				}
			}
		}
	}
	res.rewindUses()
	return nil
}

// assignHomes adds the uses with addUses unless read has, and gives a home
// slot to every value used in a block other than its own without being
// passed there as an argument. It returns the end of the homes.
func (o *Online) assignHomes() (int, error) {
	n := len(o.res.state)
	cross := o.home // marks crossing values with -2 until homes are assigned
	if !o.fused || o.late {
		if err := o.addUses(); err != nil {
			return 0, err
		}
	}
	next := o.config.ReservedSlots
	for v := range n {
		if cross[v] != -2 {
			continue
		}
		if o.value(v).Class == backend.Vector && next%2 != o.config.VectorSlotParity {
			next++
		}
		o.home[v] = next
		next += o.storage(v)
	}
	if next > o.config.SlotLimit {
		return 0, o.limitError(next)
	}
	o.paramLocs = resize(o.paramLocs, len(o.params))
	// Edge's buffers hold a move per parameter of the widest block.
	widest := 0
	for b := range o.f.blocks {
		widest = max(widest, o.paramStart[b+1]-o.paramStart[b])
	}
	if cap(o.pending) < widest {
		o.pending, o.blockers = make([]edgeMove, 0, widest), make([]int, 0, widest)
	}
	o.pool = o.pool[:0]
	for v := range o.paramSlot {
		o.paramSlot[v] = -1
	}
	return next, nil
}

func resize[T any](s []T, n int) []T {
	if cap(s) < n {
		return make([]T, n)
	}
	s = s[:n]
	clear(s)
	return s
}

// Entry places block 0's parameters: in their argument registers, and in
// their homes when they cross a block boundary.
func (o *Online) Entry() error {
	o.enterScope(0)
	var pins Pins
	locations := o.res.Locations()
	params := o.params[o.paramStart[0]:o.paramStart[1]]
	// Claim the argument registers before anything is loaded into one.
	for i, p := range params {
		if slot, _ := frameSlot(o.f.layout.Arguments[i]); slot >= 0 {
			continue
		}
		g, inRegisters, err := o.resolve(o.f.layout.Arguments[i])
		if err != nil {
			return err
		}
		if !inRegisters {
			continue
		}
		if int(g.Words) != o.words(p) {
			return invalid("entry argument %d location %v", i, o.f.layout.Arguments[i])
		}
		if err := o.res.Claim(p, g); err != nil {
			return err
		}
		pins.Add(g)
	}
	for _, p := range params {
		h := o.home[p]
		if h < 0 {
			continue
		}
		g := o.res.Group(p)
		if g.Words == 0 {
			// Stack words: load them to store them.
			var err error
			if g, err = o.res.Ensure(p, pins); err != nil {
				return err
			}
			o.write(g)
			pins.Add(g)
		}
		o.mover.Spill(p, h, g)
		locations[p].Slot, locations[p].SlotWords = h, uint8(o.storage(p))
	}
	for _, p := range params {
		if o.res.Remaining(p) == 0 {
			o.res.Release(p)
		}
	}
	return nil
}

func (o *Online) write(g Group) { o.written.Add(g) }

// liveAfter reports whether value has uses after position's.
func (o *Online) liveAfter(value int, ops []Operand) bool {
	here := 0
	for xi := range ops {
		x := &ops[xi]
		if !x.Def && x.Value == value {
			here++
		}
	}
	return o.res.Remaining(value) > here
}

// load places value, which is not resident, in to from its location.
func (o *Online) load(value int, to Group) error {
	loc := o.res.Locations()[value]
	switch {
	case loc.Slot >= 0:
		o.mover.Reload(value, to, loc.Slot)
	case loc.IncomingWords != 0:
		o.mover.LoadIncoming(value, to, loc.Incoming)
	case o.value(value).Constant:
		if err := o.mover.Rematerialize(value, to); err != nil {
			return err
		}
	default:
		return invalid("value %d has no source to load", value)
	}
	return nil
}

// place puts value in exactly target for a use at position, moving away
// whatever holds it. Registers in pins hold this position's other operands.
// A reserved target only gets a copy: the client may overwrite it during
// the instruction.
// awaits reports whether the registers of use i, among placing, hold the
// value of another of them that goes elsewhere.
func (o *Online) awaits(i int, placing []int, ops []Operand, targets []Group) bool {
	for w := range int(targets[i].Words) {
		owner := o.res.Owner(targets[i].Registers[w])
		if owner < 0 || owner == ops[i].Value {
			continue
		}
		for _, j := range placing {
			if j != i && ops[j].Value == owner {
				return true
			}
		}
	}
	return false
}

// place puts value in target, evicting the values there; at a call, one
// with a stored copy or a constant is dropped instead, to be reloaded where
// it is next used.
func (o *Online) place(value int, target Group, pins Pins, position int, copied, call bool) error {
	res := &o.res
	cur := res.Group(value)
	if cur == target {
		return nil
	}
	if copied {
		// The value stays where it is; target holds a copy for the
		// position alone.
		keep := pins
		keep.Add(target)
		keep.Add(cur)
		if err := o.clear(value, target, pins, keep, call); err != nil {
			return err
		}
		if cur.Words != 0 {
			o.mover.Move(value, target, cur)
		} else if err := o.load(value, target); err != nil {
			return err
		}
		o.write(target)
		return nil
	}
	if groupsOverlapPins(target, o.scopes.reserved(position)) {
		for i := range int(target.Words) {
			r := target.Registers[i]
			if owner := res.Owner(r); owner >= 0 {
				if pins.Contains(r) {
					return invalid("value %d and %d need register %d at once", value, owner, r)
				}
				if err := o.release(owner); err != nil {
					return err
				}
			}
		}
		if cur.Words != 0 {
			o.mover.Move(value, target, cur)
		} else if err := o.load(value, target); err != nil {
			return err
		}
		o.write(target)
		return nil
	}
	keep := pins
	keep.Add(target)
	keep.Add(cur)
	if err := o.clear(value, target, pins, keep, call); err != nil {
		return err
	}
	if cur.Words != 0 {
		o.mover.Move(value, target, cur)
		res.Release(value)
	} else if err := o.load(value, target); err != nil {
		return err
	}
	o.write(target)
	return res.Claim(value, target)
}

// keepAcross moves each value that lives past position out of the registers
// call c overwrites, to a free register outside clobbered and the position's
// fixed registers, as a spill before the call and a reload after it would
// cost more. A value it cannot move is stored, and stays only when the
// position reads it, so that placing the uses moves nothing else. Vector
// registers go first, so that their wider slots pack before the others.
func (o *Online) keepAcross(c int, ops []Operand, targets []Group, clobbered Pins) error {
	res := &o.res
	avoid := clobbered
	for i := range targets {
		if targets[i].Words != 0 {
			avoid.Add(targets[i])
		}
	}
	pinned := res.regs.pins(avoid)
	for k := range 2 * len(o.calls[c].clobbers) {
		r := o.calls[c].clobbers[k%len(o.calls[c].clobbers)]
		v := res.Owner(r)
		if v < 0 || backend.IsVectorRegister(r) != (k < len(o.calls[c].clobbers)) || !o.liveAfter(v, ops) {
			continue
		}
		if from := res.Group(v); !o.value(v).Constant && res.Locations()[v].Slot < 0 {
			// Only a free register: every occupied one is pinned.
			if to, err := res.choose(v, pinned|^res.occupancy.freeMask); err == nil {
				named := res.regs.group(to)
				o.mover.Move(v, named, from)
				res.occupancy.release(v)
				if err := res.occupancy.claim(v, to, named); err != nil {
					return err
				}
				o.write(named)
				continue
			}
			if err := res.Spill(v, from); err != nil {
				return err
			}
		}
		if !o.usedAt(v, ops) {
			res.Release(v)
		}
	}
	return nil
}

// usedAt reports whether ops read value.
func (o *Online) usedAt(value int, ops []Operand) bool {
	for i := range ops {
		if x := &ops[i]; !x.Def && x.Value == value {
			return true
		}
	}
	return false
}

// clear empties target's registers of values other than value for place.
func (o *Online) clear(value int, target Group, pins, keep Pins, call bool) error {
	res := &o.res
	for i := range int(target.Words) {
		r := target.Registers[i]
		owner := res.Owner(r)
		switch {
		case owner < 0 || owner == value:
		case pins.Contains(r):
			return invalid("value %d and %d need register %d at once", value, owner, r)
		case call && (o.value(owner).Constant || res.Locations()[owner].Slot >= 0):
			res.Release(owner)
		default:
			if err := res.Evict(r, keep); err != nil {
				return err
			}
		}
	}
	return nil
}

// release frees value's registers; a value with uses left and no other copy
// is spilled first.
func (o *Online) release(value int) error {
	g := o.res.Group(value)
	if g.Words == 0 {
		return nil
	}
	if o.res.Remaining(value) > 0 && !o.value(value).Constant && o.res.Locations()[value].Slot < 0 {
		if err := o.res.Spill(value, g); err != nil {
			return err
		}
	}
	o.res.Release(value)
	return nil
}

// enterScope blocks the registers reserved at position.
func (o *Online) enterScope(position int) {
	s := o.scopes.scope(position)
	if s == o.scope {
		return
	}
	if s < 0 {
		o.res.regs.blocked = o.blockedBase
	} else {
		o.res.regs.blocked = o.blockedIn[s]
	}
	o.scope = s
}

// fixedHere reports whether value's last use is at position, fixed to
// registers that include r: it may stay there, since the client clobbering
// the register during the instruction loses nothing.
func (o *Online) fixedHere(position, value int, r backend.Register) bool {
	fixed, uses := false, 0
	for i := o.opStart[position]; i < o.opStart[position+1]; i++ {
		if x := &o.ops[i]; !x.Def && x.Value == value {
			uses++
			fixed = fixed || groupsOverlapPins(o.targets[i], singlePins(r))
		}
	}
	return fixed && o.res.Remaining(value) == uses
}

func singlePins(r backend.Register) Pins {
	var p Pins
	p.AddRegister(r)
	return p
}

// vacate blocks the registers reserved at position and moves the values
// still in them out, so the client may use them during its instruction.
func (o *Online) vacate(position int) error {
	o.enterScope(position)
	s := o.scopes.scope(position)
	if s < 0 {
		return nil
	}
	// Only the reserved registers that hold something need a look.
	held := o.reservedIn[s] &^ o.res.occupancy.freeMask
	for ; held != 0; held &= held - 1 {
		r := o.res.regs.registers[bits.TrailingZeros64(held)]
		owner := o.res.Owner(r)
		switch {
		case owner < 0 || o.fixedHere(position, owner, r):
		case o.res.Remaining(owner) == 0 || o.home[owner] >= 0 || o.value(owner).Constant:
			o.res.Release(owner)
		default:
			if err := o.res.Evict(r, Pins{}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *Online) releaseAll() {
	occ := &o.res.occupancy
	for _, owner := range occ.owners {
		if owner >= 0 {
			occ.release(owner)
		}
	}
}

// Before places position's uses, makes room for its definitions and calls,
// and chooses its definitions' registers.
func (o *Online) Before(position int) error {
	if position < 0 || position >= len(o.posBlock) {
		return backend.ErrInvalidFunction
	}
	if err := o.before(position); err != nil {
		return fmt.Errorf("position %d: %w", position, err)
	}
	return nil
}

func (o *Online) before(position int) error {
	res := &o.res
	if b := o.posBlock[position]; b != 0 && o.blockStart[b] == position {
		o.releaseAll()
		if err := o.enterBlock(b); err != nil {
			return err
		}
	}
	if len(o.scopes.sets) != 0 {
		if err := o.vacate(position); err != nil {
			return err
		}
	}
	start := o.opStart[position]
	ops := o.ops[start:o.opStart[position+1]]
	if o.simple[position] {
		return o.beforeSimple(ops, o.groups[start:start+len(ops)])
	}
	groups, targets := o.groups[start:start+len(ops)], o.targets[start:start+len(ops)]
	clear(groups)
	c := o.callIndex[position]
	var clobbered Pins
	if c >= 0 {
		for _, r := range o.calls[c].clobbers {
			clobbered.AddRegister(r)
		}
		if err := o.keepAcross(c, ops, targets, clobbered); err != nil {
			return err
		}
	}
	var uses Pins
	// Uses with a register of their own first, so the others avoid them,
	// each once no other of them is still to leave its registers. One that
	// lives past an overwritten register is copied there.
	placing := o.placing[:0]
	for i := range ops {
		if !ops[i].Def && targets[i].Words != 0 {
			placing = append(placing, i)
		}
	}
	for len(placing) != 0 {
		k := 0
		for len(placing) > 1 && k < len(placing) && o.awaits(placing[k], placing, ops, targets) {
			k++
		}
		if k == len(placing) {
			k = 0 // a cycle: the first evicts what it waits for
		}
		i := placing[k]
		placing = append(placing[:k], placing[k+1:]...)
		x := &ops[i]
		copied := c >= 0 && groupsOverlapPins(targets[i], clobbered) && o.liveAfter(x.Value, ops)
		if err := o.place(x.Value, targets[i], uses, position, copied, c >= 0); err != nil {
			return err
		}
		groups[i] = targets[i]
		uses.Add(targets[i])
	}
	o.placing = placing
	for i := range ops {
		x := &ops[i]
		if x.Def || targets[i].Words != 0 || x.Memory {
			continue
		}
		g, err := res.Ensure(x.Value, uses)
		if err != nil {
			return err
		}
		o.write(g)
		groups[i] = g
		uses.Add(g)
	}
	// A call overwrites its clobbers: values live past it leave them, and
	// values moved for the definitions below avoid them.
	if c >= 0 {
		for _, r := range o.calls[c].clobbers {
			if owner := res.Owner(r); owner >= 0 && o.liveAfter(owner, ops) {
				if err := o.release(owner); err != nil {
					return err
				}
			}
			o.written.AddRegister(r)
		}
	}
	// A use read from memory needs a stored copy; it is consumed after the
	// instruction, so no spill before it takes its slot.
	o.inputs, o.memoryInputs = o.inputs[:0], o.memoryInputs[:0]
	for i := range ops {
		x := &ops[i]
		switch {
		case x.Def:
		case !x.Memory:
			o.inputs = append(o.inputs, x.Value)
		default:
			loc := res.Locations()[x.Value]
			if loc.Slot < 0 && loc.IncomingWords == 0 && !o.value(x.Value).Constant {
				g := res.Group(x.Value)
				if g.Words == 0 {
					return invalid("value %d has no copy to read from memory", x.Value)
				}
				if err := res.Spill(x.Value, g); err != nil {
					return err
				}
				loc = res.Locations()[x.Value]
			}
			o.memory[start+i] = loc
			o.memoryInputs = append(o.memoryInputs, x.Value)
		}
	}
	o.consume(o.inputs)
	// Definitions with a register of their own.
	var fixed Pins
	for i := range ops {
		x := &ops[i]
		if x.Def && targets[i].Words != 0 && !o.value(x.Value).Constant {
			fixed.Add(targets[i])
		}
	}
	var defs Pins
	avoid := func(a, b, c Pins) Pins {
		for w := range a.bits {
			a.bits[w] |= b.bits[w] | c.bits[w] | fixed.bits[w]
		}
		return a
	}
	for i := range ops {
		x := &ops[i]
		if !x.Def {
			continue
		}
		v := x.Value
		if o.value(v).Constant {
			continue
		}
		var g Group
		var err error
		switch {
		case targets[i].Words != 0:
			g = targets[i]
			for w := range int(g.Words) {
				owner := res.Owner(g.Registers[w])
				switch {
				case owner < 0:
				case res.Remaining(owner) > 0:
					err = res.Evict(g.Registers[w], avoid(uses, defs, clobbered))
				default:
					res.Release(owner)
				}
				if err != nil {
					return err
				}
			}
		case x.Prefer.Words != 0 && o.preferFreeGroup(x.Prefer, avoid(uses, defs, clobbered)):
			g = x.Prefer
		case x.Tied:
			u, tied := ops[x.TiedUse].Value, groups[x.TiedUse]
			taken := avoid(defs, Pins{}, Pins{})
			if groupsOverlapPins(tied, taken) {
				// Another definition needs the tied registers: tie to a copy.
				if g, err = res.ChooseGroup(o.words(u), o.value(u).Class, avoid(uses, defs, clobbered), Group{}); err != nil {
					return err
				}
				o.mover.Move(u, g, tied)
				break
			}
			g = tied
			if res.Group(u) == tied && res.Remaining(u) > 0 {
				// The use outlives the instruction that overwrites it.
				if o.home[u] >= 0 || o.value(u).Constant {
					res.Release(u)
				} else if err = res.Evict(tied.Registers[0], avoid(uses, defs, clobbered)); err != nil {
					return err
				}
			}
		case x.EarlyClobber:
			g, err = res.Choose(v, avoid(uses, defs, Pins{}))
		default:
			g, err = res.Choose(v, avoid(defs, Pins{}, Pins{}))
		}
		if err != nil {
			return err
		}
		if err := res.Claim(v, g); err != nil {
			return err
		}
		o.write(g)
		groups[i] = g
		defs.Add(g)
	}
	return nil
}

// beforeSimple is before for a position without fixed or early-clobber
// operands, memory uses, calls or clobbers: it loads the uses, consumes them
// and chooses the definitions' registers, with pins as index masks.
func (o *Online) beforeSimple(ops []Operand, groups []Group) error {
	res := &o.res
	occ := &res.occupancy
	var pinned uint64
	for i := range ops {
		x := &ops[i]
		if x.Def {
			continue
		}
		v := x.Value
		if occ.held[v].group.Words == 0 {
			if err := res.ensure(v, pinned); err != nil {
				return err
			}
		} else if int(occ.held[v].group.Words) != res.words(v) {
			return invalid("value %d is resident in %d registers, not %d", v, occ.held[v].group.Words, res.words(v))
		}
		m := groupMask(occ.held[v].group)
		pinned |= m
		o.writtenIndex |= m
		groups[i] = occ.held[v].named
	}
	for i := range ops {
		if x := &ops[i]; !x.Def {
			o.consumeOne(x.Value)
		}
	}
	var defined uint64
	for i := range ops {
		x := &ops[i]
		if !x.Def {
			continue
		}
		v := x.Value
		if o.value(v).Constant {
			groups[i] = Group{}
			continue
		}
		var g indexGroup
		preferred := false
		if x.Prefer.Words != 0 {
			g, preferred = o.preferFree(x.Prefer, pinned|defined)
		}
		var err error
		switch {
		case preferred:
		case x.Tied:
			g, err = o.tieSimple(ops[x.TiedUse].Value, groups[x.TiedUse], pinned, defined)
		default:
			g, err = res.choose(v, defined)
		}
		if err != nil {
			return err
		}
		named := res.regs.group(g)
		if err := occ.claim(v, g, named); err != nil {
			return err
		}
		m := groupMask(g)
		defined |= m
		o.writtenIndex |= m
		groups[i] = named
	}
	return nil
}

// preferFree returns a definition's Prefer, which read has checked, and
// whether its registers are free, unblocked and outside pinned; if so, they
// count as chosen as chooseGroup's do.
func (o *Online) preferFree(prefer Group, pinned uint64) (indexGroup, bool) {
	res := &o.res
	g, _ := res.regs.indices(prefer)
	m := groupMask(g)
	if res.occupancy.freeMask&^(pinned|res.regs.blocked)&m != m {
		return g, false
	}
	res.chosenPreserved |= res.regs.preserved & m
	return g, true
}

func (o *Online) preferFreeGroup(prefer Group, pins Pins) bool {
	_, ok := o.preferFree(prefer, o.res.regs.pins(pins))
	return ok
}

// tieSimple returns the registers of a definition tied to u, a use in tied
// whose uses at the position are consumed, as before does: tied, after
// moving u out when it outlives the instruction, or a copy of it when an
// earlier definition took tied.
func (o *Online) tieSimple(u int, tied Group, uses, defined uint64) (indexGroup, error) {
	res := &o.res
	g, _ := res.regs.indices(tied)
	if groupMask(g)&defined != 0 {
		c, err := res.chooseGroup(o.words(u), o.value(u).Class, uses|defined, indexGroup{})
		if err != nil {
			return indexGroup{}, err
		}
		o.mover.Move(u, res.regs.group(c), tied)
		return c, nil
	}
	if res.occupancy.held[u].group == g && res.state[u].remaining > 0 {
		// The use outlives the instruction that overwrites it.
		if o.home[u] >= 0 || o.value(u).Constant {
			res.occupancy.release(u)
		} else if err := res.evict(g.Indices[0], uses|defined); err != nil {
			return indexGroup{}, err
		}
	}
	return g, nil
}

// consumeOne records a use of value, keeping the slots of homes and of
// parameters that start in a slot, which an edge may still write.
func (o *Online) consumeOne(v int) {
	if o.home[v] < 0 && o.paramSlot[v] < 0 {
		o.res.consume(v)
		return
	}
	locations := o.res.Locations()
	if s := o.paramSlot[v]; s >= 0 && locations[v].Slot == s {
		locations[v].Slot = -1
	}
	o.res.consume(v)
	switch {
	case o.home[v] >= 0:
		locations[v].Slot, locations[v].SlotWords = o.home[v], uint8(o.storage(v))
	case o.paramSlot[v] >= 0 && o.res.Remaining(v) > 0:
		locations[v].Slot, locations[v].SlotWords = o.paramSlot[v], uint8(o.storage(v))
	}
}

func groupsOverlapPins(g Group, p Pins) bool {
	for i := range int(g.Words) {
		if p.Contains(g.Registers[i]) {
			return true
		}
	}
	return false
}

// Operand returns the registers Before gave operand index of position.
func (o *Online) Operand(position, index int) Group {
	if position < 0 || position >= len(o.posBlock) || index < 0 || o.opStart[position]+index >= o.opStart[position+1] {
		return Group{}
	}
	return o.groups[o.opStart[position]+index]
}

// Memory returns where Before put a Memory use.
func (o *Online) Memory(position, index int) ValueLocation {
	if position < 0 || position >= len(o.posBlock) || index < 0 || o.opStart[position]+index >= o.opStart[position+1] {
		return ValueLocation{Slot: -1}
	}
	if i := o.opStart[position] + index; o.hasMemory && o.ops[i].Memory {
		return o.memory[i]
	}
	return ValueLocation{Slot: -1}
}

// After stores position's definitions that cross a block boundary in their
// homes, frees those without uses, and consumes the uses read from memory.
func (o *Online) After(position int) error {
	if position < 0 || position >= len(o.posBlock) {
		return backend.ErrInvalidFunction
	}
	o.consume(o.memoryInputs)
	o.memoryInputs = o.memoryInputs[:0]
	start := o.opStart[position]
	for i, x := range o.ops[start:o.opStart[position+1]] {
		if !x.Def || o.value(x.Value).Constant {
			continue
		}
		if h := o.home[x.Value]; h >= 0 {
			o.mover.Spill(x.Value, h, o.groups[start+i])
		}
		if o.res.Remaining(x.Value) == 0 {
			o.res.Release(x.Value)
		}
	}
	return nil
}

// scratch returns registers for a copy of value, avoiding avoid: the
// configured scratch registers of its bank first, then the bank's others.
// Nothing lives in registers on an edge, so any of them will do.
func (o *Online) scratch(value int, avoid Pins) (Group, error) {
	g := Group{Words: uint8(o.words(value))}
	n := 0
	for _, set := range []backend.RegisterSet{o.config.Scratch, o.config.Registers} {
		for r := range set.All() {
			if n == int(g.Words) {
				break
			}
			if !o.config.Registers.Contains(r) || avoid.Contains(r) || backend.IsVectorRegister(r) != o.vector(value) ||
				set != o.config.Scratch && o.config.Blocked.Contains(r) || n == 1 && g.Registers[0] == r {
				continue
			}
			g.Registers[n] = r
			n++
		}
	}
	if n != int(g.Words) {
		return Group{}, fmt.Errorf("no register to copy value %d: %w", value, backend.ErrBackendLimit)
	}
	return g, nil
}

// entryReserved is the registers a block's parameters may not start in:
// those reserved at its first position and at the end of any predecessor,
// which its edges' moves may not write.
func (o *Online) entryReserved(b int) Pins {
	if len(o.scopes.sets) == 0 {
		return Pins{}
	}
	avoid := o.scopes.reserved(o.blockStart[b])
	for _, e := range o.preds[o.predStart[b]:o.predStart[b+1]] {
		for w, bits := range o.scopes.reserved(o.blockStart[o.edgeFrom[e]+1] - 1).bits {
			avoid.bits[w] |= bits
		}
	}
	return avoid
}

// freeGroup returns registers of value's bank outside taken and
// Config.Blocked, in Config.Registers order, or an empty group.
func (o *Online) freeGroup(value int, taken Pins) Group {
	g := Group{Words: uint8(o.words(value))}
	n := 0
	for r := range o.config.Registers.All() {
		if n == int(g.Words) {
			break
		}
		if taken.Contains(r) || o.config.Blocked.Contains(r) || backend.IsVectorRegister(r) != o.vector(value) {
			continue
		}
		g.Registers[n] = r
		n++
	}
	if n != int(g.Words) {
		return Group{}
	}
	return g
}

// establish fixes where block b's parameters start: in the registers their
// arguments occupy now when args is set (an edge being resolved) and those
// are free to take, in other free registers, or in frame slots. Parameters
// with homes start there.
func (o *Online) establish(b int, args []int) error {
	taken := o.entryReserved(b)
	var exhausted [2]bool // per bank, general then vector: no free registers left
	for i, p := range o.params[o.paramStart[b]:o.paramStart[b+1]] {
		loc := &o.paramLocs[o.paramStart[b]+i]
		*loc = paramLoc{slot: -1}
		if h := o.home[p]; h >= 0 {
			loc.slot = h
			continue
		}
		var g Group
		if args != nil {
			if cur := o.res.Group(args[i]); cur.Words != 0 && int(cur.Words) == o.words(p) &&
				backend.IsVectorRegister(cur.Registers[0]) == o.vector(p) && !groupsOverlapPins(cur, taken) {
				g = cur
			}
		}
		bank := 0
		if o.vector(p) {
			bank = 1
		}
		if g.Words == 0 && !exhausted[bank] {
			g = o.freeGroup(p, taken)
			exhausted[bank] = g.Words == 0 && o.words(p) == 1
		}
		if g.Words == 0 {
			slot, err := o.poolSlot(p, b, args, i)
			if err != nil {
				return err
			}
			loc.slot, o.paramSlot[p] = slot, slot
			continue
		}
		loc.group = g
		taken.Add(g)
	}
	o.entryKnown[b] = true
	return nil
}

// poolSlot returns a pool slot for parameter i of block b, p, that no other
// parameter of b starts in (each slot records the last block to take it):
// its argument's, when that is a parameter of another block starting in the
// pool (a pass-through edge then needs no move), else the first free one of
// its shape, else a new one.
func (o *Online) poolSlot(p, b int, args []int, i int) (int, error) {
	words, vector := o.storage(p), o.value(p).Class == backend.Vector
	fits := func(x poolSlot) bool { return x.words == words && x.vector == vector && x.block != b+1 }
	if args != nil {
		if a := args[i]; o.paramSlot[a] >= 0 && o.defBlock[a] != b {
			for k, x := range o.pool {
				if x.slot == o.paramSlot[a] && fits(x) {
					o.pool[k].block = b + 1
					return x.slot, nil
				}
			}
		}
	}
	for k, x := range o.pool {
		if fits(x) {
			o.pool[k].block = b + 1
			return x.slot, nil
		}
	}
	slot, err := o.res.AllocateValueSlot(p)
	if err != nil {
		return 0, err
	}
	o.pool = append(o.pool, poolSlot{slot: slot, words: words, vector: vector, block: b + 1})
	return slot, nil
}

// enterBlock puts block b's parameters where its entry state says, fixing
// that state first when no edge has.
func (o *Online) enterBlock(b int) error {
	if !o.entryKnown[b] {
		if err := o.establish(b, nil); err != nil {
			return err
		}
	}
	locations := o.res.Locations()
	for i, p := range o.params[o.paramStart[b]:o.paramStart[b+1]] {
		loc := o.paramLocs[o.paramStart[b]+i]
		switch {
		case loc.group.Words != 0:
			if err := o.res.Claim(p, loc.group); err != nil {
				return err
			}
		case loc.slot >= 0:
			locations[p].Slot, locations[p].SlotWords = loc.slot, uint8(o.storage(p))
		}
	}
	return nil
}

// consume records uses of values; see consumeOne.
func (o *Online) consume(values []int) {
	for _, v := range values {
		o.consumeOne(v)
	}
}

// where returns where value is now.
func (o *Online) where(value int) edgeLoc {
	if g := o.res.Group(value); g.Words != 0 {
		return edgeLoc{group: g, slot: -1}
	}
	loc := o.res.Locations()[value]
	switch {
	case loc.Slot >= 0:
		return edgeLoc{slot: loc.Slot}
	case loc.IncomingWords != 0:
		return edgeLoc{slot: -1, incoming: true}
	}
	return edgeLoc{slot: -1, constant: true}
}

// reads reports whether m's source overlaps to.
func (o *Online) reads(m edgeMove, to edgeLoc, words int) bool {
	if m.from.group.Words != 0 && to.group.Words != 0 {
		for i := range int(m.from.group.Words) {
			if to.group.Contains(m.from.group.Registers[i]) {
				return true
			}
		}
	}
	return m.from.slot >= 0 && to.slot >= 0 && overlapsSlots(to.slot, words, m.from.slot, o.storage(m.arg))
}

// Edge moves the edge's arguments to its destination's parameters, as one
// parallel move from where they are at the end of the source. The first
// edge resolved into a block fixes its entry state. Edge must be called
// after the source's last After and before another block is walked.
func (o *Online) Edge(edge int) error {
	if edge < 0 || edge >= len(o.edgeFrom) {
		return backend.ErrInvalidFunction
	}
	from, to := o.edgeFrom[edge], o.edgeTo[edge]
	args := o.args[o.argStart[edge]:o.argStart[edge+1]]
	o.resolving = edge
	if !o.entryKnown[to] {
		if err := o.establish(to, args); err != nil {
			return err
		}
	}
	pending := o.pending[:0]
	var placed Pins // registers holding their final value: moved, or in place
	locations := o.res.Locations()
	for i, a := range args {
		dst, src := o.paramLocs[o.paramStart[to]+i].edge(), o.where(a)
		// An argument in a register may also have its copy in the
		// parameter's slot already: a parameter passed back to itself.
		if dst.group.Words != 0 && dst.group == src.group || dst.group.Words == 0 && dst.slot >= 0 && (dst.slot == src.slot || dst.slot == locations[a].Slot) {
			placed.Add(dst.group)
			continue
		}
		pending = append(pending, edgeMove{arg: a, to: dst, from: src})
	}
	o.edgeDone[edge], o.edgeEmpty[edge] = true, len(pending) == 0
	if len(pending) == 0 {
		o.pending = pending
		return nil
	}
	// Scratch registers avoid both ends' reserved registers and the source's
	// last operands, which its instruction may still read.
	last := o.blockStart[from+1] - 1
	avoid := o.entryReserved(to)
	for w, bits := range o.scopes.reserved(last).bits {
		avoid.bits[w] |= bits
	}
	for i, x := range o.ops[o.opStart[last]:o.opStart[last+1]] {
		if !x.Def {
			avoid.Add(o.groups[o.opStart[last]+i])
		}
	}
	o.edgeAvoid = avoid
	temps := o.temps[:0] // slots taken to break cycles, with their words
	defer func() {
		for i := 0; i+1 < len(temps); i += 2 {
			o.res.FreeSlots(temps[i], temps[i+1])
		}
		o.pending, o.temps = pending[:0], temps[:0]
	}()
	// The moves keep their indices; done marks those made. The first move
	// not done and ready goes next, a copy between frame slots first, as
	// the moves to make shrink in order.
	n := len(pending)
	e := &o.edge
	e.begin(n, o.res.Slots())
	defer e.end(pending, o)
	for i := range pending {
		e.index(i, &pending[i], o.storage(pending[i].arg))
	}
	// busy is what a scratch register must not take: a parameter in place
	// or a source still to be read; a pending destination is free until it
	// is written.
	busy := func(except int) Pins {
		p := e.sources
		if except >= 0 {
			g := pending[except].from.group
			for k := range int(g.Words) {
				if r := g.Registers[k]; e.readers[r] == 1 {
					p.bits[r>>6] &^= 1 << (r & 63)
				}
			}
		}
		for w := range p.bits {
			p.bits[w] |= avoid.bits[w] | placed.bits[w]
		}
		return p
	}
	e.count(pending, o)
	for left := n; left > 0; {
		next := e.next(pending)
		if next >= 0 {
			m := pending[next]
			scratch := func() Pins {
				p := busy(next)
				p.Add(m.to.group)
				return p
			}
			o.pending = pending // for borrow
			if err := o.edgeMove(m, scratch); err != nil {
				return err
			}
			placed.Add(m.to.group)
			e.finish(pending, next, o)
			left--
			continue
		}
		// A cycle: copy the first move's destination aside for its readers.
		first := e.first(pending)
		dst := pending[first].to
		reader := -1
		for j := range pending {
			if j != first && !pending[j].done && o.reads(pending[j], dst, o.storage(pending[first].arg)) {
				reader = j
				break
			}
		}
		r := pending[reader]
		aside := edgeLoc{slot: -1}
		taken := busy(-1)
		for _, m := range pending {
			if !m.done {
				taken.Add(m.to.group)
			}
		}
		if g, err := o.scratch(r.arg, taken); err == nil {
			aside.group = g
		} else {
			temp, err := o.res.AllocateValueSlot(r.arg)
			if err != nil {
				return err
			}
			temps = append(temps, temp, o.storage(r.arg))
			aside.slot = temp
			e.grow(o.res.Slots())
		}
		o.pending = pending
		if err := o.edgeMove(edgeMove{arg: r.arg, to: aside, from: r.from}, func() Pins { return taken }); err != nil {
			return err
		}
		for j := range pending {
			if !pending[j].done && pending[j].from == r.from {
				e.redirect(&pending[j], aside)
			}
		}
		e.count(pending, o)
	}
	return nil
}

// edgeState indexes an edge's pending moves by the registers and slots
// their sources read and their destinations write, so that the parallel
// move takes time linear in its moves. Its tables are left clear.
type edgeState struct {
	sources Pins        // registers a pending source reads
	readers [256]int32  // per register: the pending sources reading it
	dstReg  [256]int32  // per register: 1 + the move writing it, or 0
	dstSlot []int32     // per slot: 1 + the move writing it, or 0
	blocked []int32     // per move: the other pending moves reading its destination
	ready   [2][]uint64 // moves with no blocker: between slots, and all
}

func (e *edgeState) begin(n, slots int) {
	e.grow(slots)
	e.blocked = resize(e.blocked, n)
	words := (n + 63) / 64
	for k := range e.ready {
		e.ready[k] = resize(e.ready[k], words)
		clear(e.ready[k])
	}
}

// grow makes room for slots frame slots.
func (e *edgeState) grow(slots int) {
	if len(e.dstSlot) < slots {
		e.dstSlot = append(e.dstSlot, make([]int32, slots-len(e.dstSlot))...)
	}
}

// index records move i, m, writing words storage words.
func (e *edgeState) index(i int, m *edgeMove, words int) {
	e.addSource(m.from.group)
	for k := range int(m.to.group.Words) {
		e.dstReg[m.to.group.Registers[k]] = int32(i + 1)
	}
	if m.to.group.Words == 0 && m.to.slot >= 0 {
		for w := range words {
			e.dstSlot[m.to.slot+w] = int32(i + 1)
		}
	}
}

func (e *edgeState) addSource(g Group) {
	for k := range int(g.Words) {
		r := g.Registers[k]
		e.readers[r]++
		e.sources.AddRegister(r)
	}
}

func (e *edgeState) dropSource(g Group) {
	for k := range int(g.Words) {
		r := g.Registers[k]
		if e.readers[r]--; e.readers[r] == 0 {
			e.sources.bits[r>>6] &^= 1 << (r & 63)
		}
	}
}

// read calls f once for each move other than j whose destination m's
// source overlaps.
func (e *edgeState) read(j int, m *edgeMove, words int, f func(i int)) {
	last := -1
	visit := func(i int32) {
		if i != 0 && int(i-1) != j && int(i-1) != last {
			last = int(i - 1)
			f(last)
		}
	}
	for k := range int(m.from.group.Words) {
		visit(e.dstReg[m.from.group.Registers[k]])
	}
	if m.from.group.Words == 0 && m.from.slot >= 0 {
		for w := range words {
			if s := m.from.slot + w; s < len(e.dstSlot) {
				visit(e.dstSlot[s])
			}
		}
	}
}

// count recounts every pending move's blockers and the ready sets.
func (e *edgeState) count(pending []edgeMove, o *Online) {
	for i := range pending {
		e.blocked[i] = 0
	}
	for j := range pending {
		if !pending[j].done {
			e.read(j, &pending[j], o.storage(pending[j].arg), func(i int) {
				if !pending[i].done {
					e.blocked[i]++
				}
			})
		}
	}
	for k := range e.ready {
		clear(e.ready[k])
	}
	for i := range pending {
		if !pending[i].done && e.blocked[i] == 0 {
			e.mark(pending, i)
		}
	}
}

func (e *edgeState) mark(pending []edgeMove, i int) {
	m := &pending[i]
	e.ready[1][i>>6] |= 1 << (i & 63)
	if m.to.group.Words == 0 && m.from.group.Words == 0 {
		e.ready[0][i>>6] |= 1 << (i & 63)
	}
}

// next returns the ready move to make, or -1.
func (e *edgeState) next(pending []edgeMove) int {
	for k := range e.ready {
		for w, word := range e.ready[k] {
			if word != 0 {
				return 64*w + bits.TrailingZeros64(word)
			}
		}
	}
	return -1
}

// first returns the first move not done.
func (e *edgeState) first(pending []edgeMove) int {
	for i := range pending {
		if !pending[i].done {
			return i
		}
	}
	return -1
}

// finish marks move i made: the moves it blocked lose a blocker.
func (e *edgeState) finish(pending []edgeMove, i int, o *Online) {
	m := &pending[i]
	m.done = true
	for k := range e.ready {
		e.ready[k][i>>6] &^= 1 << (i & 63)
	}
	e.dropSource(m.from.group)
	e.read(i, m, o.storage(m.arg), func(j int) {
		if !pending[j].done {
			if e.blocked[j]--; e.blocked[j] == 0 {
				e.mark(pending, j)
			}
		}
	})
}

// redirect makes m read its value from aside.
func (e *edgeState) redirect(m *edgeMove, aside edgeLoc) {
	e.dropSource(m.from.group)
	m.from = aside
	e.addSource(aside.group)
}

// end clears the tables pending's moves set.
func (e *edgeState) end(pending []edgeMove, o *Online) {
	for i := range pending {
		m := &pending[i]
		for k := range int(m.to.group.Words) {
			e.dstReg[m.to.group.Registers[k]] = 0
		}
		if m.to.group.Words == 0 && m.to.slot >= 0 {
			for w := 0; w < o.storage(m.arg) && m.to.slot+w < len(e.dstSlot); w++ {
				e.dstSlot[m.to.slot+w] = 0
			}
		}
		if !m.done {
			e.dropSource(m.from.group)
		}
	}
}

// edgeMove emits one move of an edge, through a scratch register outside
// busy() when both ends are in memory.
func (o *Online) edgeMove(m edgeMove, busy func() Pins) error {
	load := func(to Group) error {
		switch {
		case m.from.group.Words != 0:
			o.mover.Move(m.arg, to, m.from.group)
		case m.from.slot >= 0:
			o.mover.Reload(m.arg, to, m.from.slot)
		case m.from.incoming:
			o.mover.LoadIncoming(m.arg, to, o.res.Locations()[m.arg].Incoming)
		default:
			if err := o.mover.Rematerialize(m.arg, to); err != nil {
				return err
			}
		}
		o.write(to)
		return nil
	}
	if m.to.group.Words != 0 {
		// A pair overlapping its own source goes through scratch registers,
		// which must not be either.
		if m.from.group.Words == 2 && m.from.group != m.to.group && groupsOverlap(m.from.group, m.to.group) {
			avoid := busy()
			avoid.Add(m.from.group)
			g, err := o.scratch(m.arg, avoid)
			if err != nil {
				// No pair is free: through a temporary slot.
				temp, err := o.res.AllocateValueSlot(m.arg)
				if err != nil {
					return err
				}
				o.mover.Spill(m.arg, temp, m.from.group)
				o.mover.Reload(m.arg, m.to.group, temp)
				o.res.FreeSlots(temp, o.storage(m.arg))
				o.write(m.to.group)
				return nil
			}
			if err := load(g); err != nil {
				return err
			}
			o.mover.Move(m.arg, m.to.group, g)
			o.write(m.to.group)
			return nil
		}
		return load(m.to.group)
	}
	if m.from.group.Words != 0 {
		o.mover.Spill(m.arg, m.to.slot, m.from.group)
		return nil
	}
	g, err := o.scratch(m.arg, busy())
	if err != nil {
		// Every register holds a parameter: borrow one around the copy.
		return o.borrow(m, load, busy())
	}
	if err := load(g); err != nil {
		return err
	}
	o.mover.Spill(m.arg, m.to.slot, g)
	return nil
}

// borrow copies m, from memory to memory, through a register that already
// holds a parameter's final value (one in busy that no pending move still
// writes): it saves the register to a temporary slot first and restores it
// after.
func (o *Online) borrow(m edgeMove, load func(Group) error, busy Pins) error {
	if o.words(m.arg) != 1 {
		return fmt.Errorf("no register to copy value %d: %w", m.arg, backend.ErrBackendLimit)
	}
	edge := o.resolving
	to := o.edgeTo[edge]
	args := o.args[o.argStart[edge]:o.argStart[edge+1]]
	for i, held := range args {
		loc := o.paramLocs[o.paramStart[to]+i]
		if loc.group.Words != 1 || !busy.Contains(loc.group.Registers[0]) || o.vector(held) != o.vector(m.arg) || o.words(held) != 1 {
			continue
		}
		stillPending := false
		for _, p := range o.pending {
			stillPending = stillPending || !p.done && p.to.group == loc.group
		}
		if stillPending {
			continue
		}
		r := loc.group.Registers[0]
		g := SingleRegister(r)
		temp, err := o.res.AllocateValueSlot(held)
		if err != nil {
			return err
		}
		o.mover.Spill(held, temp, g)
		if err := load(g); err != nil {
			return err
		}
		o.mover.Spill(m.arg, m.to.slot, g)
		o.mover.Reload(held, g, temp)
		o.res.FreeSlots(temp, o.storage(held))
		return nil
	}
	// Or one holding a source still to be read, which it then holds again.
	for _, p := range o.pending {
		if p.done || p.from.group.Words != 1 || o.vector(p.arg) != o.vector(m.arg) || o.words(p.arg) != 1 ||
			o.edgeAvoid.Contains(p.from.group.Registers[0]) {
			continue
		}
		g := p.from.group
		temp, err := o.res.AllocateValueSlot(p.arg)
		if err != nil {
			return err
		}
		o.mover.Spill(p.arg, temp, g)
		if err := load(g); err != nil {
			return err
		}
		o.mover.Spill(m.arg, m.to.slot, g)
		o.mover.Reload(p.arg, g, temp)
		o.res.FreeSlots(temp, o.storage(p.arg))
		return nil
	}
	return fmt.Errorf("no register to copy value %d: %w", m.arg, backend.ErrBackendLimit)
}

func overlapsSlots(a, aWords, b, bWords int) bool { return a < b+bWords && b < a+aWords }

// EdgeEmpty reports whether an edge needed no moves; it is known once Edge
// has resolved it.
func (o *Online) EdgeEmpty(edge int) bool {
	return edge >= 0 && edge < len(o.edgeEmpty) && o.edgeDone[edge] && o.edgeEmpty[edge]
}

// Slots returns the frame slots used: Config.ReservedSlots, the homes, and
// the Residency's spill and temporary slots.
func (o *Online) Slots() int { return o.res.Slots() }

// Report names the allocatable registers the function writes.
func (o *Online) Report(r *abi.EmissionReport) {
	r.ClobberedRegisters, r.ClobberedFPRegisters = nil, nil
	if !o.config.Registers.Named() {
		return
	}
	for i, reg := range o.config.Registers.AppendTo(nil) {
		if !o.written.Contains(reg) && o.writtenIndex&(1<<uint(i)) == 0 {
			continue
		}
		if backend.IsVectorRegister(reg) {
			r.ClobberedFPRegisters = append(r.ClobberedFPRegisters, o.config.Registers.NameOf(reg))
		} else {
			r.ClobberedRegisters = append(r.ClobberedRegisters, o.config.Registers.NameOf(reg))
		}
	}
}

// Finish ends the function.
func (o *Online) Finish() {
	o.res.Finish()
	o.config, o.f, o.mover = Config{}, functionShape{}, nil
}
