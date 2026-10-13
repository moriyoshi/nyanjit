package regalloc

import (
	"fmt"
	"math"
	"math/bits"
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// Program points order every position's uses before its definitions, and a
// block's parameters before its first position and its edge arguments after
// its last: block b's entry is 2*Start+2b, position p's uses 2p+2b+1 and
// definitions 2p+2b+2, and its exit 2*End+2b+1.
func (a *Batch) entryPoint(b int) int { return 2*a.work.blockStart.s[b] + 2*b }
func (a *Batch) exitPoint(b int) int  { return 2*a.work.blockEnd.s[b] + 2*b + 1 }
func usePoint(b, p int) int           { return 2*p + 2*b + 1 }
func defPoint(b, p int) int           { return 2*p + 2*b + 2 }

const (
	defNone uint8 = iota
	defEntry
	defParam
	defOperand
)

type moveKind uint8

const (
	moveCopy moveKind = iota
	moveSpill
	moveLoad
)

// recordedMove is one Mover call. A spill stores to slotOf's slot, or to the
// temporary area at temp; a load reads the temporary area at temp, or value's
// memory location.
type recordedMove struct {
	kind     moveKind
	value    int
	to, from indexGroup
	slotOf   int
	temp     int
}

type functionOperand struct {
	value  int
	def    bool
	fixed  indexGroup
	tied   int // index into ops of the tied use, or -1
	early  bool
	memory bool
	loc    indexGroup
}

type resolvedEffect struct {
	argStart, argEnd int // into effectArgs
	result           indexGroup
	clobber          uint64
}

type livePair struct{ block, value, distance int }

type pendingMove struct {
	value    int
	to, from indexGroup
	temp     int // >= 0: load from the temporary area instead of from
}

// buffer is a reusable slice with its demand history.
type buffer[T any] struct {
	s []T
	h bufferHistory
}

func (b *buffer[T]) reset(n int) []T { b.s = trackedReset(&b.s, &b.h, n); return b.s }
func (b *buffer[T]) begin() []T      { return b.s[:0] }
func (b *buffer[T]) end(s []T)       { b.s = s; b.h.Observe(len(s)) }
func (b *buffer[T]) bytes() int      { return bytesOf(b.s) }
func (b *buffer[T]) drop()           { b.s = nil }
func (b *buffer[T]) decay()          { decay(&b.s, &b.h) }

type retainedBuffer interface {
	bytes() int
	drop()
	decay()
}

type functionWork struct {
	values                                    buffer[Value]
	blockStart, blockEnd, paramStart, params  buffer[int]
	posBlock, opStart                         buffer[int]
	ops                                       buffer[functionOperand]
	operands                                  buffer[Operand]
	callIndex                                 buffer[int]
	posClobber                                buffer[uint64] // per position, the registers it overwrites
	effects                                   buffer[resolvedEffect]
	effectArgs                                buffer[indexGroup]
	effectIndex                               map[*abi.CallEffect]int
	entry                                     buffer[indexGroup]
	edgeFrom, edgeTo, argStart, args          buffer[int]
	predStart, preds, succStart, succs        buffer[int]
	loopEnd, loopHeader, loopParent           buffer[int]
	pressure, loopExcess                      buffer[[2]int]
	defKind                                   buffer[uint8]
	defBlock, defPoint, defPos                buffer[int]
	useStart, usePoints, useBlocks, useCursor buffer[int]
	minPoint, maxPoint, stampIn, stampOut     buffer[int]
	stack                                     buffer[int]
	inPairs                                   buffer[livePair]
	outPairs                                  buffer[livePair]
	liveInStart, liveInValues, liveInDistance buffer[int]
	liveOutStart, liveOutValues               buffer[int]
	inMark, outMark, edgeMark                 buffer[int]
	owners                                    buffer[int]
	defGroup, sourceGroup                     buffer[indexGroup]
	held                                      buffer[occupant]
	occupancy                                 occupancy
	spilled, memory, paramMemory, needed      buffer[bool]
	incoming                                  buffer[ValueLocation]
	slot                                      buffer[int]
	startState, endState                      buffer[int]
	startValues, endValues                    buffer[int]
	startGroups, endGroups                    buffer[indexGroup]
	moves, spills                             buffer[recordedMove]
	beforeStart, edgeMoves                    buffer[int]
	spillAfter, spillBlock                    buffer[int]
	pending, loads                            buffer[pendingMove]
	slotOrder, active, freeSlots              buffer[int]

	entryMoves           [2]int
	block, now, exit     int
	temps                uint64 // registers holding copies at this position
	tempValues           [64]int
	written              uint64
	tempChunks, tempSlot int    // chunkWords-slot chunks in the temporary area
	chosenPreserved      uint64 // indices of Config.Preserved registers already taken
	returnedChunks       []int  // resolveEdge's free temporary chunks
	edgeChunks           int
	chunkWords           int // words per edge temporary: the widest stored value
	slots                int
	loops                bool // any back edge
}

func (w *functionWork) buffers() []retainedBuffer {
	return []retainedBuffer{
		&w.values,
		&w.blockStart,
		&w.blockEnd,
		&w.paramStart,
		&w.params,
		&w.posBlock,
		&w.opStart,
		&w.ops,
		&w.operands,
		&w.callIndex,
		&w.posClobber,
		&w.effects,
		&w.effectArgs,
		&w.entry,
		&w.edgeFrom,
		&w.edgeTo,
		&w.argStart,
		&w.args,
		&w.predStart,
		&w.preds,
		&w.succStart,
		&w.succs,
		&w.loopEnd,
		&w.loopHeader,
		&w.loopParent,
		&w.pressure,
		&w.loopExcess,
		&w.defKind,
		&w.defBlock,
		&w.defPoint,
		&w.defPos,
		&w.useStart,
		&w.usePoints,
		&w.useBlocks,
		&w.useCursor,
		&w.minPoint,
		&w.maxPoint,
		&w.stampIn,
		&w.stampOut,
		&w.stack,
		&w.inPairs,
		&w.outPairs,
		&w.liveInStart,
		&w.liveInValues,
		&w.liveInDistance,
		&w.liveOutStart,
		&w.liveOutValues,
		&w.inMark,
		&w.outMark,
		&w.edgeMark,
		&w.owners,
		&w.held,
		&w.defGroup,
		&w.sourceGroup,
		&w.spilled,
		&w.memory,
		&w.paramMemory,
		&w.needed,
		&w.incoming,
		&w.slot,
		&w.startState,
		&w.endState,
		&w.startValues,
		&w.endValues,
		&w.startGroups,
		&w.endGroups,
		&w.moves,
		&w.spills,
		&w.beforeStart,
		&w.edgeMoves,
		&w.spillAfter,
		&w.spillBlock,
		&w.pending,
		&w.loads,
		&w.slotOrder,
		&w.active,
		&w.freeSlots,
	}
}

func (w *functionWork) poison() {
	for _, b := range []*buffer[int]{&w.opStart, &w.posBlock, &w.blockStart, &w.beforeStart, &w.edgeMoves,
		&w.spillAfter, &w.spillBlock, &w.slot, &w.useStart, &w.usePoints} {
		s := b.s[:cap(b.s)]
		for i := range s {
			s[i] = -0x5a5a5a
		}
	}
	for i := range w.ops.s[:cap(w.ops.s)] {
		w.ops.s[:cap(w.ops.s)][i] = functionOperand{value: -0x5a5a, loc: indexGroup{Words: 255}}
	}
	for i := range w.moves.s[:cap(w.moves.s)] {
		w.moves.s[:cap(w.moves.s)][i] = recordedMove{kind: 255, value: -0x5a5a}
	}
	for i := range w.spills.s[:cap(w.spills.s)] {
		w.spills.s[:cap(w.spills.s)][i] = recordedMove{kind: 255, value: -0x5a5a}
	}
}

func groupMask(g indexGroup) uint64 {
	var m uint64
	for i := 0; i < int(g.Words); i++ {
		m |= 1 << uint(g.Indices[i])
	}
	return m
}

func (a *Batch) words(value int) int {
	v := a.work.values.s[value]
	if v.Class == backend.Float || v.Words == 0 {
		return 1
	}
	return int(v.Words)
}

func (a *Batch) storageWords(value int) int {
	if v := a.work.values.s[value]; v.StorageWords != 0 {
		return int(v.StorageWords)
	}
	return a.words(value)
}

// bankMask is the registers a value's class may not use.
func (a *Batch) bankMask(value int) uint64 {
	if class := a.work.values.s[value].Class; class == backend.Vector || class == backend.Float {
		return ^a.regs.vector
	}
	return a.regs.vector
}

func (a *Batch) limitError(required int) error {
	if a.config.LimitError != nil {
		return a.config.LimitError(required)
	}
	return backend.ErrBackendLimit
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("function allocation: "+format+": %w", append(args, backend.ErrInvalidFunction)...)
}

func (a *Batch) allocate() error {
	f, w := &a.f, &a.work
	if f.values < 0 || f.positions < 1 || f.blocks < 1 || f.edges < 0 {
		return invalid("incomplete description")
	}
	values := w.values.reset(f.values)
	// Edge temporaries hold any value, so each chunk is as wide as the
	// widest stored value, kept even for vector slot parity.
	w.chunkWords = 2
	for i := range values {
		values[i] = f.fn.Value(i)
		v := values[i]
		storage := int(v.StorageWords)
		if storage == 0 {
			storage = max(int(v.Words), 1)
		}
		if v.Words > 2 || v.Class != backend.Float && storage < int(v.Words) || v.Class != backend.Vector && storage > 2 {
			return invalid("value %d has %d register words and %d storage words", i, v.Words, storage)
		}
		w.chunkWords = max(w.chunkWords, (storage+1)&^1)
	}
	w.written, w.tempChunks, w.edgeChunks, w.chosenPreserved = 0, 0, 0, 0
	for _, step := range []func() error{a.readBlocks, a.readScopes, a.readOperands, a.readEdges, a.readCalls, a.readDefinitions,
		a.readUses, a.computeLiveness, a.measureLoops, a.scan, a.resolveEdges, a.assignSlots} {
		if err := step(); err != nil {
			return err
		}
	}
	a.placeSpills()
	return nil
}

// readScopes finds each position's reserved registers as an index mask.
func (a *Batch) readScopes() error {
	if err := readScopes(a.f.fn, a.f.positions, a.config.Registers, &a.scopes); err != nil {
		return err
	}
	a.scopeMask = resize(a.scopeMask, len(a.scopes.sets))
	for s, set := range a.scopes.sets {
		a.scopeMask[s] = a.regs.pins(set)
	}
	a.baseBlocked = a.regs.blocked
	return nil
}

// entryReserved is the registers a block's entry state must avoid: those
// reserved at its first position and at every predecessor's last, which its
// edges' moves may not write.
func (a *Batch) entryReserved(b int) uint64 {
	w := &a.work
	m := a.reservedMask(w.blockStart.s[b])
	for _, e := range w.preds.s[w.predStart.s[b]:w.predStart.s[b+1]] {
		m |= a.reservedMask(w.blockEnd.s[w.edgeFrom.s[e]] - 1)
	}
	return m
}

// reservedMask is the registers reserved at position.
func (a *Batch) reservedMask(position int) uint64 {
	if s := a.scopes.scope(position); s >= 0 {
		return a.scopeMask[s]
	}
	return 0
}

func (a *Batch) readBlocks() error {
	f, w := &a.f, &a.work
	n := f.values
	start, end := w.blockStart.reset(f.blocks), w.blockEnd.reset(f.blocks)
	paramStart := w.paramStart.reset(f.blocks + 1)
	posBlock := w.posBlock.reset(f.positions)
	params := w.params.begin()
	next := 0
	for b := range f.blocks {
		block := f.fn.Block(b)
		if block.Start != next || block.End <= block.Start || block.End > f.positions {
			return invalid("block %d covers [%d, %d) after position %d", b, block.Start, block.End, next)
		}
		start[b], end[b] = block.Start, block.End
		for p := block.Start; p < block.End; p++ {
			posBlock[p] = b
		}
		for _, v := range block.Params {
			if v < 0 || v >= n {
				return invalid("block %d parameter %d", b, v)
			}
			params = append(params, v)
		}
		paramStart[b+1] = len(params)
		next = block.End
	}
	w.params.end(params)
	if next != f.positions {
		return invalid("blocks end at %d of %d positions", next, f.positions)
	}
	return nil
}

func (a *Batch) readOperands() error {
	f, w := &a.f, &a.work
	opStart := w.opStart.reset(f.positions + 1)
	posClobber := w.posClobber.reset(f.positions)
	ops := w.ops.begin()
	if cap(w.operands.s) < 8 {
		w.operands.s = make([]Operand, 0, 8)
	}
	for p := range f.positions {
		list := f.fn.Operands(p, w.operands.s[:0])
		if len(list) > cap(w.operands.s) {
			w.operands.s = make([]Operand, 0, 2*len(list))
		}
		w.operands.h.Observe(len(list))
		base := len(ops)
		for _, o := range list {
			if o.Value < 0 || o.Value >= f.values {
				return invalid("position %d operand value %d", p, o.Value)
			}
			tied := -1
			if o.Tied {
				if !o.Def || o.TiedUse < 0 || o.TiedUse >= len(list) || list[o.TiedUse].Def {
					return invalid("position %d tie to operand %d", p, o.TiedUse)
				}
				tied = base + o.TiedUse
			}
			if o.Memory && (o.Def || o.Fixed.Words != 0) || o.Tied && list[o.TiedUse].Memory {
				return invalid("position %d reads value %d from memory with a register constraint", p, o.Value)
			}
			if o.Prefer.Words != 0 {
				if err := checkPrefer(&a.regs, p, &o, a.words(o.Value), a.work.values.s[o.Value].Class); err != nil {
					return err
				}
			}
			fixed := indexGroup{}
			if o.Fixed.Words != 0 {
				var ok bool
				if fixed, ok = a.regs.indices(o.Fixed); !ok || !validGroup(fixed, a.regs.count) {
					return invalid("position %d fixed group %v", p, o.Fixed)
				}
			}
			ops = append(ops, functionOperand{value: o.Value, def: o.Def, fixed: fixed, tied: tied, early: o.EarlyClobber, memory: o.Memory})
		}
		posClobber[p] = a.regs.mask(f.fn.Clobbers(p))
		opStart[p+1] = len(ops)
	}
	w.ops.end(ops)
	return nil
}

func validGroup(g indexGroup, registers int) bool {
	if g.Words < 1 || g.Words > 2 {
		return false
	}
	for i := 0; i < int(g.Words); i++ {
		if g.Indices[i] < 0 || g.Indices[i] >= registers {
			return false
		}
	}
	return g.Words == 1 || g.Indices[0] != g.Indices[1]
}

// checkPrefer validates an operand's Prefer: a group of registers of the
// value's class, words wide, on a definition.
func checkPrefer(regs *registerIndex, p int, x *Operand, words int, class backend.Class) error {
	g, ok := regs.indices(x.Prefer)
	if !ok || !x.Def || !validGroup(g, regs.count) || int(g.Words) != words {
		return invalid("position %d preferred group %v", p, x.Prefer)
	}
	if vector := groupMask(g)&regs.vector != 0; vector != (class == backend.Float || class == backend.Vector) || vector && groupMask(g)&^regs.vector != 0 {
		return invalid("position %d preferred group %v for class %d", p, x.Prefer, class)
	}
	return nil
}

func (a *Batch) readEdges() error {
	f, w := &a.f, &a.work
	from, to := w.edgeFrom.reset(f.edges), w.edgeTo.reset(f.edges)
	argStart := w.argStart.reset(f.edges + 1)
	args := w.args.begin()
	for e := range f.edges {
		edge := f.fn.Edge(e)
		if edge.From < 0 || edge.From >= f.blocks || edge.To <= 0 || edge.To >= f.blocks {
			return invalid("edge %d from %d to %d", e, edge.From, edge.To)
		}
		if want := w.paramStart.s[edge.To+1] - w.paramStart.s[edge.To]; len(edge.Args) != want {
			return invalid("edge %d passes %d arguments to %d parameters", e, len(edge.Args), want)
		}
		from[e], to[e] = edge.From, edge.To
		for _, v := range edge.Args {
			if v < 0 || v >= f.values {
				return invalid("edge %d argument %d", e, v)
			}
			args = append(args, v)
		}
		argStart[e+1] = len(args)
	}
	w.args.end(args)
	csr := func(start, list *buffer[int], key []int) {
		s := start.reset(f.blocks + 1)
		for _, b := range key {
			s[b+1]++
		}
		for b := range f.blocks {
			s[b+1] += s[b]
		}
		l := list.reset(f.edges)
		cursor := w.stack.reset(f.blocks)
		for e, b := range key {
			l[s[b]+cursor[b]] = e
			cursor[b]++
		}
	}
	csr(&w.predStart, &w.preds, to)
	csr(&w.succStart, &w.succs, from)
	a.findLoops()
	return nil
}

// findLoops treats an edge to the same or an earlier block in layout order as
// a back edge, and the blocks from its target to its source as a loop headed
// by the target. A loop that overlaps an enclosing one without nesting in it
// is cut off at the enclosing loop's end. Loops only guide spill choices.
func (a *Batch) findLoops() {
	f, w := &a.f, &a.work
	end, header, parent := w.loopEnd.reset(f.blocks), w.loopHeader.reset(f.blocks), w.loopParent.reset(f.blocks)
	for b := range end {
		end[b] = -1
	}
	for e, from := range w.edgeFrom.s {
		if to := w.edgeTo.s[e]; to <= from {
			end[to] = max(end[to], from)
		}
	}
	open := w.stack.begin()
	for b := range f.blocks {
		for len(open) != 0 && end[open[len(open)-1]] < b {
			open = open[:len(open)-1]
		}
		parent[b] = -1
		if end[b] >= 0 {
			if len(open) != 0 {
				top := open[len(open)-1]
				end[b], parent[b] = min(end[b], end[top]), top
			}
			open = append(open, b)
		}
		header[b] = -1
		if len(open) != 0 {
			header[b] = open[len(open)-1]
		}
	}
	w.stack.end(open)
	w.loops = slices.ContainsFunc(end, func(e int) bool { return e >= 0 })
}

// bank is 1 for a value in the vector bank and 0 otherwise.
func (a *Batch) bank(value int) int {
	if class := a.work.values.s[value].Class; class == backend.Vector || class == backend.Float {
		return 1
	}
	return 0
}

// measureLoops estimates each loop's register pressure, per bank, from the
// values whose live hulls cross it, and records by how many words it exceeds
// the allocatable registers.
func (a *Batch) measureLoops() error {
	f, w := &a.f, &a.work
	excess := w.loopExcess.reset(f.blocks)
	if !w.loops {
		return nil
	}
	span := a.exitPoint(f.blocks-1) + 2
	diff := w.pressure.reset(span)
	for v := range f.values {
		if w.values.s[v].Constant || !w.needed.s[v] || w.maxPoint.s[v] <= w.minPoint.s[v] {
			continue
		}
		bank, words := a.bank(v), a.words(v)
		diff[w.minPoint.s[v]][bank] += words
		diff[w.maxPoint.s[v]+1][bank] -= words
	}
	usable := ^a.regs.blocked
	if a.regs.count < 64 {
		usable &= 1<<uint(a.regs.count) - 1
	}
	vector := a.regs.vector
	capacity := [2]int{bits.OnesCount64(usable &^ vector), bits.OnesCount64(usable & vector)}
	var live [2]int
	point := 0
	for b := range f.blocks {
		var peak [2]int
		for ; point <= a.exitPoint(b); point++ {
			for k := range live {
				live[k] += diff[point][k]
				peak[k] = max(peak[k], live[k])
			}
		}
		excess[b] = peak
	}
	// Fold each block's peak into its innermost loop, and each loop's into
	// its parent's; a loop's blocks all follow its header.
	for b := f.blocks - 1; b > 0; b-- {
		into := w.loopHeader.s[b]
		if into == b {
			into = w.loopParent.s[b]
		}
		if into >= 0 {
			for k := range excess[b] {
				excess[into][k] = max(excess[into][k], excess[b][k])
			}
		}
	}
	for b := range f.blocks {
		if w.loopEnd.s[b] < 0 {
			excess[b] = [2]int{}
			continue
		}
		for k := range excess[b] {
			excess[b][k] = max(0, excess[b][k]-capacity[k])
		}
	}
	return nil
}

// usedInLoop reports whether value is used anywhere in the loop headed by h,
// edge arguments included.
func (a *Batch) usedInLoop(v, h int) bool {
	w := &a.work
	uses := w.usePoints.s[w.useStart.s[v]:w.useStart.s[v+1]]
	i, _ := slices.BinarySearch(uses, a.entryPoint(h))
	return i < len(uses) && uses[i] <= a.exitPoint(w.loopEnd.s[h])
}

func (a *Batch) registerIndex(name string) int {
	for i, n := range a.names[:a.regs.count] {
		if n != "" && n == name {
			return i
		}
	}
	return -1
}

// resolveLocation converts an abi location to registers, or to incoming
// stack words.
func (a *Batch) resolveLocation(loc abi.ValueLocation) (indexGroup, ValueLocation, error) {
	group := indexGroup{Indices: [2]int{-1, -1}}
	incoming := ValueLocation{Slot: -1}
	if len(loc.Words) > 2 {
		return group, incoming, backend.ErrBackendLimit
	}
	registers := 0
	for i, word := range loc.Words {
		if word.Storage == abi.Frame {
			return group, incoming, invalid("frame location %v outside the entry arguments", loc)
		}
		if word.Storage == abi.Stack {
			incoming.Incoming[i] = word.StackOffset
			incoming.IncomingWords++
			continue
		}
		index := a.registerIndex(word.Register)
		if index < 0 {
			return group, incoming, invalid("register %q is not allocatable", word.Register)
		}
		group.Indices[i] = index
		registers++
	}
	if registers != 0 && incoming.IncomingWords != 0 || registers == 2 && group.Indices[0] == group.Indices[1] {
		return group, incoming, invalid("location %v", loc)
	}
	group.Words = uint8(registers)
	return group, incoming, nil
}

func (a *Batch) resolveEffect(effect *abi.CallEffect) (int, error) {
	w := &a.work
	r := resolvedEffect{argStart: len(w.effectArgs.s)}
	for _, loc := range effect.ArgumentLocations {
		group, _, err := a.resolveLocation(loc)
		if err != nil {
			return 0, err
		}
		w.effectArgs.s = append(w.effectArgs.s, group)
	}
	r.argEnd = len(w.effectArgs.s)
	result, incoming, err := a.resolveLocation(effect.ResultLocation)
	if err != nil {
		return 0, err
	}
	if incoming.IncomingWords != 0 {
		return 0, invalid("call result on the stack")
	}
	r.result = result
	for _, list := range [][]string{effect.ClobberedRegisters, effect.ClobberedFPRegisters} {
		for _, name := range list {
			if index := a.registerIndex(name); index >= 0 {
				r.clobber |= 1 << uint(index)
			}
		}
	}
	w.effects.s = append(w.effects.s, r)
	return len(w.effects.s) - 1, nil
}

// readCalls applies call effects, returns and the entry layout as fixed
// registers.
func (a *Batch) readCalls() error {
	f, w := &a.f, &a.work
	callIndex := w.callIndex.reset(f.positions)
	w.effects.s, w.effectArgs.s = w.effects.begin(), w.effectArgs.begin()
	if w.effectIndex == nil {
		w.effectIndex = make(map[*abi.CallEffect]int)
	}
	clear(w.effectIndex)
	fix := func(p int, op *functionOperand, group indexGroup) error {
		if op.memory {
			return invalid("position %d reads value %d from memory at an abi register", p, op.value)
		}
		if op.fixed.Words != 0 && op.fixed != group {
			return invalid("position %d operand %d fixed twice", p, op.value)
		}
		op.fixed = group
		return nil
	}
	var result indexGroup
	resultChecked := false
	for p := range f.positions {
		callIndex[p] = -1
		ops := w.ops.s[w.opStart.s[p]:w.opStart.s[p+1]]
		if effect := f.fn.Call(p); effect != nil {
			index, ok := w.effectIndex[effect]
			if !ok {
				var err error
				if index, err = a.resolveEffect(effect); err != nil {
					return err
				}
				w.effectIndex[effect] = index
			}
			callIndex[p] = index
			r := w.effects.s[index]
			arg := r.argStart
			for i := range ops {
				if ops[i].def || arg == r.argEnd {
					continue
				}
				if group := w.effectArgs.s[arg]; group.Words != 0 {
					if err := fix(p, &ops[i], group); err != nil {
						return err
					}
				}
				arg++
			}
			if arg != r.argEnd {
				return invalid("call at %d has fewer uses than arguments", p)
			}
			if r.result.Words != 0 {
				for i := range ops {
					if ops[i].def {
						if err := fix(p, &ops[i], r.result); err != nil {
							return err
						}
						break
					}
				}
			}
		}
		if f.fn.Returns(p) {
			if !resultChecked {
				group, incoming, err := a.resolveLocation(f.layout.Result)
				if err != nil {
					return err
				}
				if group.Words == 0 || incoming.IncomingWords != 0 {
					return invalid("result location %v", f.layout.Result)
				}
				result, resultChecked = group, true
			}
			found := false
			for i := range ops {
				if !ops[i].def {
					if err := fix(p, &ops[i], result); err != nil {
						return err
					}
					found = true
					break
				}
			}
			if !found {
				return invalid("return at %d has no use", p)
			}
		}
		for i := range ops {
			if g := ops[i].fixed; g.Words != 0 && int(g.Words) != a.words(ops[i].value) {
				return invalid("position %d value %d fixed to %d registers", p, ops[i].value, g.Words)
			}
		}
	}
	params := w.params.s[w.paramStart.s[0]:w.paramStart.s[1]]
	if len(params) != len(f.layout.Arguments) {
		return invalid("block 0 has %d parameters for %d entry arguments", len(params), len(f.layout.Arguments))
	}
	entry := w.entry.reset(len(params))
	incoming := w.incoming.reset(f.values)
	for i := range incoming {
		incoming[i] = ValueLocation{Slot: -1}
	}
	for i, x := range params {
		loc := f.layout.Arguments[i]
		if slot, err := frameSlot(loc); err != nil {
			return err
		} else if slot >= 0 {
			// It starts in a slot the client reserved, which stays its own.
			if len(loc.Words) != a.storageWords(x) || slot+len(loc.Words) > a.config.ReservedSlots {
				return invalid("entry argument %d location %v", i, loc)
			}
			entry[i], incoming[x] = indexGroup{Indices: [2]int{-1, -1}}, ValueLocation{Slot: slot, SlotWords: uint8(len(loc.Words))}
			continue
		}
		group, in, err := a.resolveLocation(loc)
		if err != nil {
			return err
		}
		words := int(group.Words) + int(in.IncomingWords)
		if words != a.words(x) {
			return invalid("entry argument %d has %d words for value %d", i, words, x)
		}
		entry[i], incoming[x] = group, in
	}
	return nil
}

func (a *Batch) readDefinitions() error {
	f, w := &a.f, &a.work
	kind := w.defKind.reset(f.values)
	block, point, pos := w.defBlock.reset(f.values), w.defPoint.reset(f.values), w.defPos.reset(f.values)
	define := func(v, b, at, p int, k uint8) error {
		if kind[v] != defNone {
			return invalid("value %d defined twice", v)
		}
		kind[v], block[v], point[v], pos[v] = k, b, at, p
		return nil
	}
	for b := range f.blocks {
		k := defParam
		if b == 0 {
			k = defEntry
		}
		for i, x := range w.params.s[w.paramStart.s[b]:w.paramStart.s[b+1]] {
			// A parameter's defPos is its index among the block's.
			if err := define(x, b, a.entryPoint(b), i, k); err != nil {
				return err
			}
		}
	}
	for p := range f.positions {
		b := w.posBlock.s[p]
		for _, op := range w.ops.s[w.opStart.s[p]:w.opStart.s[p+1]] {
			if op.def {
				if err := define(op.value, b, defPoint(b, p), p, defOperand); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// readUses lists every value's use points in order: operand uses, and edge
// arguments at the exit of the edge's source. A parameter is needed when an
// operand reads it or it is an argument for a needed parameter; arguments for
// other parameters are not uses, and those parameters get no place.
func (a *Batch) readUses() error {
	f, w := &a.f, &a.work
	n := f.values
	needed := w.needed.reset(n)
	work := w.stack.begin()
	for _, op := range w.ops.s {
		if !op.def && !needed[op.value] {
			needed[op.value] = true
			if w.defKind.s[op.value] == defParam {
				work = append(work, op.value)
			}
		}
	}
	for len(work) != 0 {
		x := work[len(work)-1]
		work = work[:len(work)-1]
		b, i := w.defBlock.s[x], w.defPos.s[x]
		for _, e := range w.preds.s[w.predStart.s[b]:w.predStart.s[b+1]] {
			if arg := w.args.s[w.argStart.s[e]+i]; !needed[arg] {
				needed[arg] = true
				if w.defKind.s[arg] == defParam {
					work = append(work, arg)
				}
			}
		}
	}
	w.stack.end(work)
	start := w.useStart.reset(n + 1)
	for _, op := range w.ops.s {
		if !op.def {
			start[op.value+1]++
		}
	}
	for e := range f.edges {
		to := w.edgeTo.s[e]
		for i, v := range w.args.s[w.argStart.s[e]:w.argStart.s[e+1]] {
			if needed[w.params.s[w.paramStart.s[to]+i]] {
				start[v+1]++
			}
		}
	}
	for v := range n {
		if start[v+1] != 0 && w.defKind.s[v] == defNone {
			return invalid("value %d is used but never defined", v)
		}
		start[v+1] += start[v]
	}
	points, blocks := w.usePoints.reset(start[n]), w.useBlocks.reset(start[n])
	cursor := w.useCursor.reset(n)
	add := func(v, point, b int) {
		points[start[v]+cursor[v]], blocks[start[v]+cursor[v]] = point, b
		cursor[v]++
	}
	for b := range f.blocks {
		for p := w.blockStart.s[b]; p < w.blockEnd.s[b]; p++ {
			for _, op := range w.ops.s[w.opStart.s[p]:w.opStart.s[p+1]] {
				if !op.def {
					add(op.value, usePoint(b, p), b)
				}
			}
		}
		exit := a.exitPoint(b)
		for _, e := range w.succs.s[w.succStart.s[b]:w.succStart.s[b+1]] {
			to := w.edgeTo.s[e]
			for i, v := range w.args.s[w.argStart.s[e]:w.argStart.s[e+1]] {
				if needed[w.params.s[w.paramStart.s[to]+i]] {
					add(v, exit, b)
				}
			}
		}
	}
	clear(cursor)
	return nil
}

// firstUseDistance is how far into block b value is first used, or a large
// distance when the block only passes it through.
func (a *Batch) firstUseDistance(b, v int) int {
	w := &a.work
	entry, exit := a.entryPoint(b), a.exitPoint(b)
	uses := w.usePoints.s[w.useStart.s[v]:w.useStart.s[v+1]]
	i, _ := slices.BinarySearch(uses, entry)
	if i < len(uses) && uses[i] <= exit {
		return uses[i] - entry
	}
	return exit - entry + 64
}

// computeLiveness walks back from each use to the definition, recording the
// blocks a value is live into and out of.
func (a *Batch) computeLiveness() error {
	f, w := &a.f, &a.work
	n := f.values
	stampIn, stampOut := w.stampIn.reset(f.blocks), w.stampOut.reset(f.blocks)
	minPoint, maxPoint := w.minPoint.reset(n), w.maxPoint.reset(n)
	inPairs, outPairs := w.inPairs.begin(), w.outPairs.begin()
	stack := w.stack.begin()
	for v := range n {
		db, dp := w.defBlock.s[v], w.defPoint.s[v]
		low, high := dp, dp
		if w.defKind.s[v] == defParam {
			for _, e := range w.preds.s[w.predStart.s[db]:w.predStart.s[db+1]] {
				exit := a.exitPoint(w.edgeFrom.s[e])
				low, high = min(low, exit), max(high, exit)
			}
		}
		stamp := v + 1
		var failure error
		markOut := func(b int) {
			if stampOut[b] != stamp {
				stampOut[b] = stamp
				high = max(high, a.exitPoint(b))
				outPairs = append(outPairs, livePair{block: b, value: v})
			}
		}
		markIn := func(b int) {
			if stampIn[b] == stamp || failure != nil {
				return
			}
			if b == db || b == 0 {
				failure = invalid("value %d is used where its definition does not reach", v)
				return
			}
			stampIn[b] = stamp
			low = min(low, a.entryPoint(b))
			inPairs = append(inPairs, livePair{block: b, value: v, distance: a.firstUseDistance(b, v)})
			stack = append(stack, b)
		}
		for i := w.useStart.s[v]; i < w.useStart.s[v+1]; i++ {
			u, ub := w.usePoints.s[i], w.useBlocks.s[i]
			high = max(high, u)
			if u == a.exitPoint(ub) {
				markOut(ub)
			}
			if ub != db || u < dp {
				markIn(ub)
			}
		}
		for len(stack) != 0 && failure == nil {
			b := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, e := range w.preds.s[w.predStart.s[b]:w.predStart.s[b+1]] {
				p := w.edgeFrom.s[e]
				markOut(p)
				if p != db {
					markIn(p)
				}
			}
		}
		if failure != nil {
			return failure
		}
		stack = stack[:0]
		minPoint[v], maxPoint[v] = low, high
	}
	w.stack.end(stack)
	w.inPairs.end(inPairs)
	w.outPairs.end(outPairs)
	csr := func(start, values, distances *buffer[int], pairs []livePair) {
		s := start.reset(f.blocks + 1)
		for _, pair := range pairs {
			s[pair.block+1]++
		}
		for b := range f.blocks {
			s[b+1] += s[b]
		}
		vs := values.reset(len(pairs))
		var ds []int
		if distances != nil {
			ds = distances.reset(len(pairs))
		}
		cursor := w.stampIn.reset(f.blocks)
		for _, pair := range pairs {
			at := s[pair.block] + cursor[pair.block]
			vs[at] = pair.value
			if ds != nil {
				ds[at] = pair.distance
			}
			cursor[pair.block]++
		}
	}
	csr(&w.liveInStart, &w.liveInValues, &w.liveInDistance, w.inPairs.s)
	csr(&w.liveOutStart, &w.liveOutValues, nil, w.outPairs.s)
	return nil
}

func (a *Batch) liveIn(b int) []int {
	w := &a.work
	return w.liveInValues.s[w.liveInStart.s[b]:w.liveInStart.s[b+1]]
}

func (a *Batch) liveOut(b int) []int {
	w := &a.work
	return w.liveOutValues.s[w.liveOutStart.s[b]:w.liveOutStart.s[b+1]]
}

// nextUse is value's next use at or after the current point in this block,
// or past the block's exit by the distance to its first use in a successor.
func (a *Batch) nextUse(v int) int {
	w := &a.work
	i, end := w.useCursor.s[v], w.useStart.s[v+1]
	if i < w.useStart.s[v] {
		i = w.useStart.s[v]
	}
	for i < end && w.usePoints.s[i] < w.now {
		i++
	}
	w.useCursor.s[v] = i
	if i < end && w.usePoints.s[i] <= w.exit {
		return w.usePoints.s[i]
	}
	if w.outMark.s[v] == w.block {
		return w.exit + 1 + a.successorDistance(w.block, v)
	}
	return math.MaxInt
}

func (a *Batch) successorDistance(b, v int) int {
	w := &a.work
	best := math.MaxInt / 2
	for _, e := range w.succs.s[w.succStart.s[b]:w.succStart.s[b+1]] {
		if slices.Contains(w.args.s[w.argStart.s[e]:w.argStart.s[e+1]], v) {
			return 0
		}
		s := w.edgeTo.s[e]
		live := a.liveIn(s)
		if i, found := slices.BinarySearch(live, v); found {
			best = min(best, w.liveInDistance.s[w.liveInStart.s[s]+i])
		}
	}
	return best
}

// liveAfter reports whether value is needed after the current position.
func (a *Batch) liveAfter(v int) bool {
	w := &a.work
	a.nextUse(v)
	i, end := w.useCursor.s[v], w.useStart.s[v+1]
	for i < end && w.usePoints.s[i] == w.now {
		i++
	}
	return i < end && w.usePoints.s[i] <= w.exit || w.outMark.s[v] == w.block
}

func (a *Batch) markSpilled(v int) {
	w := &a.work
	if !w.memory.s[v] {
		w.spilled.s[v], w.memory.s[v] = true, true
	}
}

func (a *Batch) evict(v int, _ indexGroup) error {
	a.markSpilled(v)
	return nil
}

// choose finds registers for value outside avoid, evicting values. While a
// Config.Preserved register has not been taken yet, a free other register
// or the eviction of a value that needs no store comes first, as for
// Residency.ChooseGroup: the first use of a preserved register costs the
// client a save and a restore.
func (a *Batch) choose(v int, avoid uint64) (indexGroup, error) {
	w := &a.work
	pinned := avoid | a.regs.blocked | a.bankMask(v) | w.temps
	if unchosen := a.regs.preserved &^ w.chosenPreserved &^ pinned; unchosen != 0 {
		clean := pinned | unchosen
		for index, owner := range w.occupancy.owners {
			if owner >= 0 && !w.memory.s[owner] {
				clean |= 1 << uint(index)
			}
		}
		if group, err := w.occupancy.reserve(a.words(v), clean, indexGroup{}, a.nextUse, a.evict); err == nil {
			return group, nil
		}
	}
	group, err := w.occupancy.reserve(a.words(v), pinned, indexGroup{}, a.nextUse, a.evict)
	if err != nil {
		return group, fmt.Errorf("allocate %d-word class %d group: %w", a.words(v), a.work.values.s[v].Class, err)
	}
	return group, nil
}

// chooseFree finds free registers for value outside avoid without evicting.
func (a *Batch) chooseFree(v int, avoid uint64) (indexGroup, bool) {
	w := &a.work
	pinned := avoid | a.regs.blocked | a.bankMask(v) | w.temps
	words := a.words(v)
	group := indexGroup{Indices: [2]int{-1, -1}, Words: uint8(words)}
	count := 0
	for index, owner := range w.occupancy.owners {
		if owner < 0 && pinned&(1<<uint(index)) == 0 {
			group.Indices[count] = index
			if count++; count == words {
				return group, true
			}
		}
	}
	return indexGroup{}, false
}

func (a *Batch) free(g indexGroup) bool {
	for i := 0; i < int(g.Words); i++ {
		if a.work.occupancy.owners[g.Indices[i]] >= 0 || a.work.temps&(1<<uint(g.Indices[i])) != 0 {
			return false
		}
	}
	return true
}

func (a *Batch) record(move recordedMove) {
	w := &a.work
	if move.kind != moveSpill {
		w.written |= groupMask(move.to)
	}
	w.moves.s = append(w.moves.s, move)
}

func (a *Batch) copyTo(v int, to, from indexGroup) {
	a.record(recordedMove{kind: moveCopy, value: v, to: to, from: from, slotOf: -1, temp: -1})
}

func (a *Batch) loadTo(v int, to indexGroup, temp int) {
	a.record(recordedMove{kind: moveLoad, value: v, to: to, slotOf: -1, temp: temp})
}

func (a *Batch) spillFrom(v int, from indexGroup, slotOf, temp int) {
	a.record(recordedMove{kind: moveSpill, value: v, from: from, slotOf: slotOf, temp: temp})
}

func (a *Batch) claim(v int, g indexGroup) error {
	if err := a.work.occupancy.claim(v, g, Group{}); err != nil {
		return invalid("value %d cannot take registers %v", v, g)
	}
	a.work.chosenPreserved |= groupMask(g) & a.regs.preserved
	return nil
}

func (a *Batch) setTemp(g indexGroup, v int) {
	w := &a.work
	for i := 0; i < int(g.Words); i++ {
		w.temps |= 1 << uint(g.Indices[i])
		w.tempValues[g.Indices[i]] = v
	}
}

func (a *Batch) holdsTemp(g indexGroup, v int) bool {
	w := &a.work
	for i := 0; i < int(g.Words); i++ {
		if w.temps&(1<<uint(g.Indices[i])) == 0 || w.tempValues[g.Indices[i]] != v {
			return false
		}
	}
	return g.Words != 0
}

func (a *Batch) scan() error {
	f, w := &a.f, &a.work
	n := f.values
	w.occupancy.owners = w.owners.reset(a.regs.count)
	w.occupancy.empty()
	w.occupancy.held = w.held.reset(n)
	spilled, memory := w.spilled.reset(n), w.memory.reset(n)
	w.paramMemory.reset(n)
	w.defGroup.reset(n)
	slot := w.slot.reset(n)
	inMark, outMark := w.inMark.reset(n), w.outMark.reset(n)
	for v := range n {
		slot[v], inMark[v], outMark[v] = -1, -1, -1
		memory[v] = w.values.s[v].Constant || w.incoming.s[v].IncomingWords != 0
		spilled[v] = false
	}
	// Entry arguments in frame slots are already stored there.
	for _, x := range w.params.s[w.paramStart.s[0]:w.paramStart.s[1]] {
		if s := w.incoming.s[x].Slot; s >= 0 {
			a.paramInMemory(x)
			slot[x] = s
		}
	}
	clear(w.useCursor.s)
	w.moves.s = w.moves.begin()
	beforeStart := w.beforeStart.reset(f.positions + 1)
	startState, endState := w.startState.reset(f.blocks+1), w.endState.reset(f.blocks+1)
	w.startValues.s, w.startGroups.s = w.startValues.begin(), w.startGroups.begin()
	w.endValues.s, w.endGroups.s = w.endValues.begin(), w.endGroups.begin()
	for b := range f.blocks {
		a.regs.blocked = a.baseBlocked | a.entryReserved(b)
		if err := a.startBlock(b); err != nil {
			return err
		}
		startState[b+1] = len(w.startValues.s)
		for p := w.blockStart.s[b]; p < w.blockEnd.s[b]; p++ {
			beforeStart[p] = len(w.moves.s)
			if err := a.position(b, p); err != nil {
				return fmt.Errorf("position %d: %w", p, err)
			}
		}
		a.endBlock(b)
		endState[b+1] = len(w.endValues.s)
	}
	beforeStart[f.positions] = len(w.moves.s)
	w.startValues.end(w.startValues.s)
	w.startGroups.end(w.startGroups.s)
	w.endValues.end(w.endValues.s)
	w.endGroups.end(w.endGroups.s)
	return nil
}

func (a *Batch) releaseAll() {
	r := &a.work.occupancy
	for index, v := range r.owners {
		if v >= 0 {
			r.held[v] = occupant{}
			r.owners[index] = -1
			r.freeMask |= 1 << uint(index&63)
		}
	}
}

func (a *Batch) paramInMemory(x int) {
	w := &a.work
	w.spilled.s[x], w.memory.s[x], w.paramMemory.s[x] = true, true, true
	w.defGroup.s[x] = indexGroup{}
}

// startBlock sets the registers at block b's entry: block 0's from the entry
// layout, others from the end of an allocated predecessor, preferring the
// previous block.
func (a *Batch) startBlock(b int) error {
	w := &a.work
	w.block, w.now, w.exit, w.temps = b, a.entryPoint(b), a.exitPoint(b), 0
	a.releaseAll()
	for _, v := range a.liveIn(b) {
		w.inMark.s[v] = b
	}
	for _, v := range a.liveOut(b) {
		w.outMark.s[v] = b
	}
	params := w.params.s[w.paramStart.s[b]:w.paramStart.s[b+1]]
	if b == 0 {
		w.entryMoves[0] = len(w.moves.s)
		for i, x := range params {
			g := w.entry.s[i]
			if g.Words == 0 || !w.needed.s[x] {
				continue // incoming stack words, or unused
			}
			if !a.free(g) {
				return invalid("entry arguments share register %v", g)
			}
			if groupMask(g)&a.regs.blocked == 0 {
				if err := a.claim(x, g); err != nil {
					return err
				}
				w.defGroup.s[x] = g
				continue
			}
			if moved, ok := a.chooseFree(x, groupMask(g)|a.entryMask()); ok {
				a.copyTo(x, moved, g)
				if err := a.claim(x, moved); err != nil {
					return err
				}
				w.defGroup.s[x] = moved
			} else {
				w.defGroup.s[x] = g
				a.markSpilled(x)
			}
		}
		w.entryMoves[1] = len(w.moves.s)
	} else {
		chosen := -1
		for _, e := range w.preds.s[w.predStart.s[b]:w.predStart.s[b+1]] {
			if from := w.edgeFrom.s[e]; from < b && (chosen < 0 || from == b-1) {
				chosen = e
			}
		}
		if chosen >= 0 {
			from := w.edgeFrom.s[chosen]
			values := w.endValues.s[w.endState.s[from]:w.endState.s[from+1]]
			groups := w.endGroups.s[w.endState.s[from]:w.endState.s[from+1]]
			reserved := a.entryReserved(b)
			for i, v := range values {
				// A value in a register the block reserves enters it in memory.
				if w.inMark.s[v] == b && groupMask(groups[i])&reserved == 0 {
					if err := a.claim(v, groups[i]); err != nil {
						return err
					}
				}
			}
			if w.loopEnd.s[b] >= 0 {
				a.trimLoopEntry(b)
			}
			args := w.args.s[w.argStart.s[chosen]:w.argStart.s[chosen+1]]
			for i, x := range params {
				if !w.needed.s[x] {
					continue
				}
				arg := args[i]
				if j := slices.Index(values, arg); j >= 0 { // live-in values hold their registers already
					g := groups[j]
					if a.free(g) && int(g.Words) == a.words(x) && groupMask(g)&(a.bankMask(x)|a.regs.blocked) == 0 {
						if err := a.claim(x, g); err != nil {
							return err
						}
						w.defGroup.s[x] = g
						continue
					}
				}
				if err := a.placeParam(x); err != nil {
					return err
				}
			}
		} else {
			for _, x := range params {
				if !w.needed.s[x] {
					continue
				}
				if err := a.placeParam(x); err != nil {
					return err
				}
			}
		}
		for _, v := range a.liveIn(b) {
			if w.occupancy.held[v].group.Words == 0 {
				a.markSpilled(v)
			}
		}
	}
	for index, v := range w.occupancy.owners {
		if v >= 0 && w.occupancy.held[v].group.First() == index {
			w.startValues.s = append(w.startValues.s, v)
			w.startGroups.s = append(w.startGroups.s, w.occupancy.held[v].group)
		}
	}
	return nil
}

// trimLoopEntry keeps values that loop b never reads out of registers on
// entry to it, furthest next use first, while its pressure exceeds a bank:
// one of them would be evicted inside the loop and reloaded on every back
// edge.
func (a *Batch) trimLoopEntry(b int) {
	w := &a.work
	for bank, excess := range w.loopExcess.s[b] {
		for excess > 0 {
			victim, distance := -1, -1
			for index, v := range w.occupancy.owners {
				if v < 0 || w.occupancy.held[v].group.First() != index || a.bank(v) != bank || a.usedInLoop(v, b) {
					continue
				}
				if next := a.nextUse(v); next > distance {
					victim, distance = v, next
				}
			}
			if victim < 0 {
				break
			}
			w.occupancy.release(victim)
			excess -= a.words(victim)
		}
	}
}

// entryMask is the registers holding block 0's entry arguments.
func (a *Batch) entryMask() uint64 {
	var m uint64
	for _, g := range a.work.entry.s {
		m |= groupMask(g)
	}
	return m
}

func (a *Batch) placeParam(x int) error {
	if g, ok := a.chooseFree(x, 0); ok {
		a.work.defGroup.s[x] = g
		return a.claim(x, g)
	}
	a.paramInMemory(x)
	return nil
}

func (a *Batch) usedAt(ops []functionOperand, v int) bool {
	for _, op := range ops {
		if !op.def && !op.memory && op.value == v {
			return true
		}
	}
	return false
}

// usePins is the registers of values used at this position.
func (a *Batch) usePins(ops []functionOperand) uint64 {
	var m uint64
	for _, op := range ops {
		if !op.def {
			m |= groupMask(a.work.occupancy.held[op.value].group) | groupMask(op.loc)
		}
	}
	return m
}

// relocate moves value out of its registers, avoiding avoid; it evicts
// another value when evict is set and drops value (keeping its memory copy)
// otherwise.
func (a *Batch) relocate(v int, avoid uint64, evict bool) error {
	w := &a.work
	old := w.occupancy.held[v].group
	avoid |= groupMask(old)
	var moved indexGroup
	ok := false
	if moved, ok = a.chooseFree(v, avoid); !ok && evict {
		var err error
		if moved, err = a.choose(v, avoid); err != nil {
			return err
		}
		ok = true
	}
	w.occupancy.release(v)
	if !ok {
		a.markSpilled(v)
		return nil
	}
	a.copyTo(v, moved, old)
	return a.claim(v, moved)
}

// moveTo copies value to registers moved and makes them its home.
func (a *Batch) moveTo(v int, moved indexGroup) error {
	old := a.work.occupancy.release(v)
	a.copyTo(v, moved, old)
	return a.claim(v, moved)
}

func (a *Batch) position(b, p int) error {
	w := &a.work
	ops := w.ops.s[w.opStart.s[p]:w.opStart.s[p+1]]
	w.now, w.temps = usePoint(b, p), 0
	reserved := a.reservedMask(p)
	a.regs.blocked = a.baseBlocked | reserved
	clobber := w.posClobber.s[p]
	var need, defFixed uint64
	if index := w.callIndex.s[p]; index >= 0 {
		clobber |= w.effects.s[index].clobber
	}
	// A use read from memory needs a stored copy, not registers.
	for _, op := range ops {
		if op.memory {
			a.markSpilled(op.value)
		}
	}
	for i := range ops {
		op := &ops[i]
		op.loc = indexGroup{}
		if op.fixed.Words == 0 {
			continue
		}
		m := groupMask(op.fixed)
		if op.def {
			if defFixed&m != 0 {
				return invalid("position %d defines two values in %v", p, op.fixed)
			}
			defFixed |= m
			continue
		}
		for _, other := range ops[:i] {
			if !other.def && other.value != op.value && groupMask(other.fixed)&m != 0 {
				return invalid("position %d needs %v for two values", p, op.fixed)
			}
		}
		need |= m
	}
	for i, op := range ops {
		if op.tied < 0 {
			continue
		}
		for _, other := range ops[i+1:] {
			if other.tied == op.tied {
				return invalid("position %d ties two definitions to one use", p)
			}
		}
	}
	kill := clobber | defFixed
	// Vacate the registers reserved here for the client, but those of this
	// position's fixed uses.
	for index := range a.regs.count {
		v := w.occupancy.owners[index]
		if reserved&(1<<uint(index)) == 0 || v < 0 {
			continue
		}
		g := w.occupancy.held[v].group
		fixedHere, used := false, a.usedAt(ops, v)
		for _, op := range ops {
			fixedHere = fixedHere || !op.def && op.value == v && op.fixed == g
		}
		switch {
		case fixedHere:
		case used:
			if err := a.relocate(v, need|kill, true); err != nil {
				return err
			}
		case a.liveAfter(v):
			if err := a.relocate(v, need|kill, false); err != nil {
				return err
			}
		default:
			w.occupancy.release(v)
		}
	}
	// The client may overwrite its reserved registers during the instruction,
	// like a clobber: a fixed use there keeps a copy elsewhere if it lives on.
	kill |= reserved
	// Clear the registers this position needs or overwrites.
	for index := range a.regs.count {
		bit := uint64(1) << uint(index)
		v := w.occupancy.owners[index]
		if (need|kill)&bit == 0 || v < 0 {
			continue
		}
		g := w.occupancy.held[v].group
		fixedHere := false
		for _, op := range ops {
			fixedHere = fixedHere || !op.def && op.value == v && op.fixed == g
		}
		used := a.usedAt(ops, v)
		// A value the position overwrites but needs afterwards moves to a
		// free safe register, or stays and is dropped after the position
		// (its memory copy is stored after its definition).
		switch {
		case fixedHere:
			if groupMask(g)&kill != 0 && a.liveAfter(v) {
				if moved, ok := a.chooseFree(v, need|kill|a.usePins(ops)); ok {
					if err := a.moveTo(v, moved); err != nil {
						return err
					}
					a.setTemp(g, v)
				} else {
					a.markSpilled(v)
				}
			}
		case used && groupMask(g)&need != 0:
			moved, err := a.choose(v, need|kill|a.usePins(ops))
			if err != nil {
				if moved, err = a.choose(v, need|a.usePins(ops)); err != nil {
					return err
				}
			}
			if err := a.moveTo(v, moved); err != nil {
				return err
			}
		case used:
			if groupMask(g)&kill != 0 && a.liveAfter(v) {
				if moved, ok := a.chooseFree(v, need|kill|a.usePins(ops)); ok {
					if err := a.moveTo(v, moved); err != nil {
						return err
					}
				} else {
					a.markSpilled(v)
				}
			}
		default:
			if err := a.relocate(v, need|kill, false); err != nil {
				return err
			}
		}
	}
	// Fixed uses.
	for i := range ops {
		op := &ops[i]
		if op.def || op.fixed.Words == 0 {
			continue
		}
		v, fixed := op.value, op.fixed
		g := w.occupancy.held[v].group
		op.loc = fixed
		if g == fixed || a.holdsTemp(fixed, v) {
			continue
		}
		if !a.free(fixed) {
			return invalid("position %d cannot free %v", p, fixed)
		}
		only := !a.liveAfter(v)
		for j, other := range ops {
			only = only && (j == i || other.def || other.value != v)
		}
		// The client may overwrite a reserved register during the
		// instruction, so a value placed there is only a temporary copy.
		inReserved := groupMask(fixed)&reserved != 0
		if g.Words != 0 {
			a.copyTo(v, fixed, g)
			if only && !inReserved {
				w.occupancy.release(v)
				if err := a.claim(v, fixed); err != nil {
					return err
				}
			} else {
				a.setTemp(fixed, v)
			}
			continue
		}
		if !w.memory.s[v] {
			return invalid("value %d has no copy to load", v)
		}
		a.loadTo(v, fixed, -1)
		if only || groupMask(fixed)&(kill|reserved) != 0 {
			a.setTemp(fixed, v)
		} else if err := a.claim(v, fixed); err != nil {
			return err
		}
	}
	// Other uses.
	for i := range ops {
		op := &ops[i]
		if op.def || op.fixed.Words != 0 || op.memory {
			continue
		}
		v := op.value
		if g := w.occupancy.held[v].group; g.Words != 0 {
			op.loc = g
			continue
		}
		if !w.memory.s[v] {
			return invalid("value %d has no copy to load", v)
		}
		avoid := need | a.usePins(ops)
		g, err := a.choose(v, avoid|kill)
		if err != nil {
			// Read it from an overwritten register; it stays in memory.
			if g, err = a.choose(v, avoid); err != nil {
				return err
			}
		}
		a.loadTo(v, g, -1)
		if err := a.claim(v, g); err != nil {
			return err
		}
		op.loc = g
	}
	// A tied use whose value survives keeps a copy for the definition, and
	// one whose registers another definition needs is tied to a copy.
	var tiedRegisters uint64
	for i := range ops {
		op := &ops[i]
		if !op.def || op.tied < 0 {
			continue
		}
		use := &w.ops.s[op.tied]
		v := use.value
		if groupMask(use.loc)&(defFixed|tiedRegisters) != 0 {
			if use.fixed.Words != 0 && groupMask(use.loc)&defFixed != 0 {
				return invalid("position %d ties a definition to fixed result register %v", p, use.loc)
			}
			g, err := a.choose(v, need|kill|tiedRegisters|a.usePins(ops))
			if err != nil {
				return err
			}
			a.copyTo(v, g, use.loc)
			use.loc = g
			a.setTemp(g, v)
			tiedRegisters |= groupMask(g)
			continue
		}
		tiedRegisters |= groupMask(use.loc)
		if w.occupancy.held[v].group == use.loc && a.liveAfter(v) {
			if err := a.relocate(v, need|kill|a.usePins(ops), true); err != nil {
				return err
			}
			a.setTemp(use.loc, v)
		}
	}
	var useMask uint64
	for _, op := range ops {
		if !op.def {
			useMask |= groupMask(op.loc)
		}
	}
	for _, op := range ops {
		if !op.def && !a.liveAfter(op.value) && w.occupancy.held[op.value].group.Words != 0 {
			w.occupancy.release(op.value)
		}
	}
	w.temps = 0
	w.written |= clobber
	for index := range a.regs.count {
		if v := w.occupancy.owners[index]; v >= 0 && kill&(1<<uint(index)) != 0 {
			a.markSpilled(v)
			w.occupancy.release(v)
		}
	}
	// Definitions.
	w.now = defPoint(b, p)
	var defined uint64
	for pass := range 2 {
		if err := a.definitions(p, ops, useMask, pass == 0, &defined); err != nil {
			return err
		}
	}
	for _, op := range ops {
		if op.def && w.occupancy.held[op.value].group.Words != 0 && !a.liveAfter(op.value) {
			w.occupancy.release(op.value)
		}
	}
	return nil
}

// definitions places the fixed and tied definitions (constrained) or the
// others.
// Registers already holding this position's definitions are pinned.
func (a *Batch) definitions(p int, ops []functionOperand, useMask uint64, constrained bool, defined *uint64) error {
	w := &a.work
	for i := range ops {
		op := &ops[i]
		if !op.def || (op.fixed.Words != 0 || op.tied >= 0) != constrained {
			continue
		}
		v := op.value
		if w.values.s[v].Constant {
			continue
		}
		var g indexGroup
		switch {
		case op.fixed.Words != 0:
			g = op.fixed
			if !a.free(g) {
				return invalid("position %d result register %v is occupied", p, g)
			}
		case op.tied >= 0:
			g = w.ops.s[op.tied].loc
			if !a.free(g) {
				return invalid("position %d tied register %v is occupied", p, g)
			}
		default:
			avoid := *defined
			if op.early {
				avoid |= useMask
			}
			var err error
			if g, err = a.choose(v, avoid); err != nil {
				return err
			}
		}
		if err := a.claim(v, g); err != nil {
			return err
		}
		w.defGroup.s[v], op.loc = g, g
		w.written |= groupMask(g)
		*defined |= groupMask(g)
	}
	return nil
}

func (a *Batch) endBlock(b int) {
	w := &a.work
	w.now = a.exitPoint(b) + 1
	for index, v := range w.occupancy.owners {
		if v < 0 || w.occupancy.held[v].group.First() != index {
			continue
		}
		if w.outMark.s[v] == b {
			w.endValues.s = append(w.endValues.s, v)
			w.endGroups.s = append(w.endGroups.s, w.occupancy.held[v].group)
		}
	}
}

// resolveEdges records each edge's parallel moves from its source's exit
// registers to its destination's entry registers and parameters.
func (a *Batch) resolveEdges() error {
	f, w := &a.f, &a.work
	edgeMoves := w.edgeMoves.reset(f.edges + 1)
	sourceGroup := w.sourceGroup.reset(f.values)
	mark := w.edgeMark.reset(f.values)
	for v := range mark {
		mark[v] = -1
	}
	for e := range f.edges {
		edgeMoves[e] = len(w.moves.s)
		if err := a.resolveEdge(e, sourceGroup, mark); err != nil {
			return err
		}
	}
	edgeMoves[f.edges] = len(w.moves.s)
	w.moves.end(w.moves.s)
	a.regs.blocked = a.baseBlocked
	return nil
}

func (a *Batch) resolveEdge(e int, sourceGroup []indexGroup, mark []int) error {
	w := &a.work
	from, to := w.edgeFrom.s[e], w.edgeTo.s[e]
	// Both ends' reserved registers are the client's.
	reserved := a.reservedMask(w.blockEnd.s[from]-1) | a.reservedMask(w.blockStart.s[to])
	a.regs.blocked = a.baseBlocked | reserved
	var occupied uint64
	for i, v := range w.endValues.s[w.endState.s[from]:w.endState.s[from+1]] {
		g := w.endGroups.s[w.endState.s[from]+i]
		sourceGroup[v], mark[v] = g, e
		occupied |= groupMask(g)
	}
	source := func(v int) indexGroup {
		if mark[v] == e {
			return sourceGroup[v]
		}
		return indexGroup{}
	}
	pending, loads := w.pending.begin(), w.loads.begin()
	type memoryMove struct{ arg, param int }
	params := w.params.s[w.paramStart.s[to]:w.paramStart.s[to+1]]
	args := w.args.s[w.argStart.s[e]:w.argStart.s[e+1]]
	isParam := func(v int) bool { return w.defKind.s[v] == defParam && w.defBlock.s[v] == to }
	for i, v := range w.startValues.s[w.startState.s[to]:w.startState.s[to+1]] {
		if isParam(v) {
			continue
		}
		g, s := w.startGroups.s[w.startState.s[to]+i], source(v)
		switch {
		case s == g:
		case s.Words != 0:
			pending = append(pending, pendingMove{value: v, to: g, from: s, temp: -1})
		default:
			loads = append(loads, pendingMove{value: v, to: g, temp: -1})
		}
	}
	var memoryMoves []memoryMove
	var stores []memoryMove // register to a parameter's slot
	for i, x := range params {
		if !w.needed.s[x] {
			continue
		}
		arg := args[i]
		d, s := w.defGroup.s[x], source(arg)
		switch {
		case w.paramMemory.s[x] && s.Words != 0:
			stores = append(stores, memoryMove{arg, x})
		case w.paramMemory.s[x]:
			memoryMoves = append(memoryMoves, memoryMove{arg, x})
		case s == d:
		case s.Words != 0:
			pending = append(pending, pendingMove{value: arg, to: d, from: s, temp: -1})
		default:
			loads = append(loads, pendingMove{value: arg, to: d, temp: -1})
		}
	}
	w.pending.h.Observe(len(pending))
	w.loads.h.Observe(len(loads))
	// Temporary chunks of this edge: a borrowed register's is returned once
	// the register is restored, so later borrows reuse it.
	chunks, returned := 0, w.returnedChunks[:0]
	takeChunk := func() int {
		if n := len(returned); n != 0 {
			temp := returned[n-1]
			returned = returned[:n-1]
			return temp
		}
		chunks++
		w.edgeChunks = max(w.edgeChunks, chunks)
		return w.chunkWords * (chunks - 1)
	}
	defer func() { w.returnedChunks = returned[:0] }()
	// withRegister runs body with registers for value: free at the source's
	// exit, a scratch register, or one borrowed and restored around it.
	withRegister := func(v int, body func(t indexGroup)) error {
		bank, words := a.bankMask(v), a.words(v)
		usable := ^(a.regs.blocked | bank) & a.registerMask()
		t, ok := freeGroup(words, usable&^occupied)
		if !ok {
			t, ok = freeGroup(words, a.regs.scratch&a.baseBlocked&^reserved&^bank&a.registerMask())
		}
		if ok {
			body(t)
			return nil
		}
		// Borrow whole values' registers, completing any free ones.
		mask := usable &^ occupied
		var borrowed []int
		values := w.endValues.s[w.endState.s[from]:w.endState.s[from+1]]
		groups := w.endGroups.s[w.endState.s[from]:w.endState.s[from+1]]
		for i := 0; i < len(values) && bits.OnesCount64(mask) < words; i++ {
			if m := groupMask(groups[i]); m&^usable == 0 {
				borrowed = append(borrowed, i)
				mask |= m
			}
		}
		if t, ok = freeGroup(words, mask); !ok {
			return fmt.Errorf("edge %d needs a register to copy value %d: %w", e, v, backend.ErrBackendLimit)
		}
		temps := make([]int, len(borrowed))
		for j, i := range borrowed {
			temps[j] = takeChunk()
			a.spillFrom(values[i], groups[i], -1, temps[j])
		}
		body(t)
		for j, i := range borrowed {
			a.loadTo(values[i], groups[i], temps[j])
		}
		returned = append(returned, temps...)
		return nil
	}
	// A parameter slot this edge writes may also be read, when the old
	// parameter is an argument: save it first and read the copy.
	type savedSlot struct{ value, temp int }
	var saved []savedSlot
	savedTemp := func(v int) int {
		for _, s := range saved {
			if s.value == v {
				return s.temp
			}
		}
		return -1
	}
	for _, x := range params {
		if !w.needed.s[x] || !w.paramMemory.s[x] || savedTemp(x) >= 0 {
			continue
		}
		read := slices.ContainsFunc(memoryMoves, func(m memoryMove) bool { return m.arg == x }) ||
			slices.ContainsFunc(loads, func(m pendingMove) bool { return m.value == x })
		if !read {
			continue
		}
		temp := takeChunk()
		if err := withRegister(x, func(t indexGroup) {
			a.loadTo(x, t, -1)
			a.spillFrom(x, t, -1, temp)
		}); err != nil {
			return err
		}
		saved = append(saved, savedSlot{x, temp})
	}
	for i := range loads {
		loads[i].temp = savedTemp(loads[i].value)
	}
	for _, m := range stores {
		a.spillFrom(m.arg, source(m.arg), m.param, -1)
	}
	for _, m := range memoryMoves {
		if err := withRegister(m.arg, func(t indexGroup) {
			a.loadTo(m.arg, t, savedTemp(m.arg))
			a.spillFrom(m.arg, t, m.param, -1)
		}); err != nil {
			return err
		}
	}
	// Register moves, breaking cycles through a scratch register or a
	// temporary slot.
	reads := func(skip int, m uint64) bool {
		for j, other := range pending {
			if j != skip && other.temp < 0 && groupMask(other.from)&m != 0 {
				return true
			}
		}
		return false
	}
	for len(pending) != 0 {
		progressed := false
		for i := 0; i < len(pending); i++ {
			m := pending[i]
			if reads(i, groupMask(m.to)) || m.temp < 0 && groupMask(m.to)&groupMask(m.from) != 0 {
				continue
			}
			if m.temp >= 0 {
				a.loadTo(m.value, m.to, m.temp)
			} else {
				a.copyTo(m.value, m.to, m.from)
			}
			pending = slices.Delete(pending, i, i+1)
			i--
			progressed = true
		}
		if progressed {
			continue
		}
		// Every remaining destination is read by another move: save the
		// first move's destination contents for their readers.
		blocked := groupMask(pending[0].to)
		reader := 0 // a pair overlapping its own destination
		for j, other := range pending {
			if j != 0 && other.temp < 0 && groupMask(other.from)&blocked != 0 {
				reader = j
				break
			}
		}
		r := &pending[reader]
		if scratch, ok := freeGroup(int(r.from.Words), a.regs.scratch&a.baseBlocked&^reserved&^a.bankMask(r.value)&a.registerMask()); ok && !readsGroup(pending, scratch) {
			a.copyTo(r.value, scratch, r.from)
			r.from = scratch
			continue
		}
		temp := takeChunk()
		a.spillFrom(r.value, r.from, -1, temp)
		r.temp = temp
	}
	for _, m := range loads {
		a.loadTo(m.value, m.to, m.temp)
	}
	w.pending.end(pending[:0])
	w.loads.end(loads[:0])
	return nil
}

func readsGroup(pending []pendingMove, g indexGroup) bool {
	for _, m := range pending {
		if m.temp < 0 && groupMask(m.from)&groupMask(g) != 0 {
			return true
		}
	}
	return false
}

// freeGroup picks the lowest words registers from mask.
func freeGroup(words int, mask uint64) (indexGroup, bool) {
	g := indexGroup{Indices: [2]int{-1, -1}, Words: uint8(words)}
	for i := 0; i < words; i++ {
		if mask == 0 {
			return indexGroup{}, false
		}
		index := bits.TrailingZeros64(mask)
		g.Indices[i] = index
		mask &^= 1 << uint(index)
	}
	return g, true
}

func (a *Batch) registerMask() uint64 {
	if a.regs.count == 64 {
		return math.MaxUint64
	}
	return 1<<uint(a.regs.count) - 1
}

// assignSlots gives spilled values frame slots by live range, reusing the
// slots of values whose ranges have ended, then places the temporary area
// edge moves use.
func (a *Batch) assignSlots() error {
	c, w := &a.config, &a.work
	order := w.slotOrder.begin()
	for v := range w.spilled.s {
		if w.spilled.s[v] && w.incoming.s[v].Slot < 0 { // not in a frame slot already
			order = append(order, v)
		}
	}
	slices.SortFunc(order, func(x, y int) int {
		if d := w.minPoint.s[x] - w.minPoint.s[y]; d != 0 {
			return d
		}
		return x - y
	})
	w.slotOrder.end(order)
	active, free := w.active.begin(), w.freeSlots.begin()
	next := c.ReservedSlots
	for _, v := range order {
		start := w.minPoint.s[v]
		for len(active) != 0 && w.maxPoint.s[active[0]] < start {
			other := active[0]
			for word := range a.storageWords(other) {
				free = append(free, w.slot.s[other]+word)
			}
			active = a.popEnd(active)
		}
		words := a.storageWords(v)
		base, ok := -1, false
		switch {
		case w.values.s[v].Class == backend.Vector:
			if base, free, ok = takeFreeRange(free, words, c.VectorSlotParity); !ok {
				if next%2 != c.VectorSlotParity {
					next++
				}
			}
		case words == 2:
			base, free, ok = takeFreePair(free)
		case words == 1 && len(free) != 0:
			base, free, ok = free[len(free)-1], free[:len(free)-1], true
		}
		if !ok {
			base = next
			next += words
		}
		if next > c.SlotLimit {
			return a.limitError(next)
		}
		w.slot.s[v] = base
		active = a.pushEnd(active, v)
	}
	w.active.end(active)
	w.freeSlots.end(free)
	w.tempSlot = -1
	if w.edgeChunks != 0 {
		if next%2 != c.VectorSlotParity {
			next++
		}
		w.tempSlot = next
		next += w.chunkWords * w.edgeChunks
		if next > c.SlotLimit {
			return a.limitError(next)
		}
	}
	w.slots = next
	return nil
}

// pushEnd and popEnd keep active a min-heap of values by the end of their
// live ranges.
func (a *Batch) pushEnd(heap []int, v int) []int {
	end := a.work.maxPoint.s
	heap = append(heap, v)
	for i := len(heap) - 1; i > 0; {
		parent := (i - 1) / 2
		if end[heap[parent]] <= end[heap[i]] {
			break
		}
		heap[parent], heap[i] = heap[i], heap[parent]
		i = parent
	}
	return heap
}

func (a *Batch) popEnd(heap []int) []int {
	end := a.work.maxPoint.s
	last := len(heap) - 1
	heap[0] = heap[last]
	heap = heap[:last]
	for i := 0; ; {
		smallest, left, right := i, 2*i+1, 2*i+2
		if left < len(heap) && end[heap[left]] < end[heap[smallest]] {
			smallest = left
		}
		if right < len(heap) && end[heap[right]] < end[heap[smallest]] {
			smallest = right
		}
		if smallest == i {
			return heap
		}
		heap[i], heap[smallest] = heap[smallest], heap[i]
		i = smallest
	}
}

// placeSpills lists the store of each spilled value right after its
// definition: after its position, or at its block's entry for a parameter.
// Position stores come first in spills, then block stores.
func (a *Batch) placeSpills() {
	f, w := &a.f, &a.work
	after, atBlock := w.spillAfter.reset(f.positions+1), w.spillBlock.reset(f.blocks+1)
	stores := func(v int) bool {
		return w.spilled.s[v] && !w.paramMemory.s[v] && w.defGroup.s[v].Words != 0
	}
	total := 0
	for v := range w.spilled.s {
		if !stores(v) {
			continue
		}
		total++
		if w.defKind.s[v] == defOperand {
			after[w.defPos.s[v]+1]++
		} else {
			atBlock[w.defBlock.s[v]+1]++
		}
	}
	for p := range f.positions {
		after[p+1] += after[p]
	}
	atBlock[0] = after[f.positions]
	for b := range f.blocks {
		atBlock[b+1] += atBlock[b]
	}
	spills := w.spills.reset(total)
	afterCursor, blockCursor := w.stampIn.reset(f.positions), w.stampOut.reset(f.blocks)
	for v := range w.spilled.s {
		if !stores(v) {
			continue
		}
		move := recordedMove{kind: moveSpill, value: v, from: w.defGroup.s[v], slotOf: v, temp: -1}
		if w.defKind.s[v] == defOperand {
			p := w.defPos.s[v]
			spills[after[p]+afterCursor[p]] = move
			afterCursor[p]++
		} else {
			b := w.defBlock.s[v]
			spills[atBlock[b]+blockCursor[b]] = move
			blockCursor[b]++
		}
	}
}
