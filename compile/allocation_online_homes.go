package compile

import (
	"iter"
	mathbits "math/bits"
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
	"github.com/moriyoshi/nyanjit/ir"
)

// Variable homes on the regalloc.Allocator protocol. A home register belongs
// to compile while its region is active, so the description reserves each
// region's home registers in a scope over the positions the region covers. A
// scope boundary is a position of its own reserving both regions' registers:
// Online vacates them before it, and the walk then stores the homes that
// change register and reloads the new ones, as the direct walk's
// transitionHomes does. A store to a register home is a use fixed to the home
// register, which Online places as a copy; slot homes and loads are ordinary
// operands the walk completes with a store, reload or move.

// onlineRun is a run of positions [start, end) in one home region; a run of
// one position at a scope boundary reserves both regions' registers.
type onlineRun struct {
	start, end   int
	region, from int // from is the previous region at a boundary, or -1
}

// nextHomeRegion returns the region a scope boundary enters.
func nextHomeRegion(plan *machine.HomePlan, ins *machineInstruction) int {
	next := int(ins.Imm)
	if ins.Arithmetic == ir.OpScopeLeave {
		next = plan.Parents[next]
	}
	return next
}

// homeAt returns the home of ins's variable part in region, with its slot
// adjusted to the part.
func homeAt(plan *machine.HomePlan, region int, ins *machineInstruction, registerBits uint8) machine.Home {
	h, ok := plan.Lookup(region, ins.Variable)
	if !ok {
		h = machine.Home{Slot: plan.Slots[ins.Variable], Register: -1}
	}
	h.Slot += int(ins.Imm) * int(64/registerBits)
	return h
}

// onlineHomeRuns appends the runs of s.program's positions, the final one
// included, to runs.
func onlineHomeRuns(s *onlineSetup, runs []onlineRun) []onlineRun {
	p := &s.program
	region, start := p.HomeRegion, 0
	for pos := range p.Instructions {
		ins := &p.Instructions[pos]
		if ins.Op != machine.SelectedHomeBoundary {
			continue
		}
		next := nextHomeRegion(p.HomePlan, ins)
		if start < pos {
			runs = append(runs, onlineRun{start: start, end: pos, region: region, from: -1})
		}
		runs = append(runs, onlineRun{start: pos, end: pos + 1, region: next, from: region})
		region, start = next, pos+1
	}
	return append(runs, onlineRun{start: start, end: len(p.Instructions) + 1, region: region, from: -1})
}

// homeMask is a set of registers by number, as HomePlan.ReservedRegions
// keeps them: testing and combining sets take no search.
type homeMask [4]uint64

func (m homeMask) contains(r physReg) bool { return m[r>>6]&(1<<(r&63)) != 0 }
func (m *homeMask) add(r physReg)          { m[r>>6] |= 1 << (r & 63) }
func (m homeMask) empty() bool             { return m == homeMask{} }

// all visits m's registers in number order.
func (m homeMask) all() iter.Seq[physReg] {
	return func(yield func(physReg) bool) {
		for w, bits := range m {
			for ; bits != 0; bits &= bits - 1 {
				if !yield(physReg(64*w + mathbits.TrailingZeros64(bits))) {
					return
				}
			}
		}
	}
}

// set returns m as a RegisterSet, for a Scope.
func (m homeMask) set() backend.RegisterSet {
	var list [64]backend.Register
	n := 0
	for r := range m.all() {
		if n < len(list) {
			list[n] = r
			n++
		}
	}
	return backend.NewRegisterSet(list[:n]...)
}

// registerMask returns registers as a homeMask.
func registerMask(registers backend.RegisterSet) homeMask {
	var m homeMask
	for r := range registers.All() {
		m.add(r)
	}
	return m
}

// homeRegisters returns the home registers of region among allocatable,
// and those of from too when from >= 0.
func homeRegisters(plan *machine.HomePlan, allocatable homeMask, region, from int) homeMask {
	var m homeMask
	if plan.ReservedRegions == nil {
		for r := range allocatable.all() {
			if plan.RegisterReserved(region, r) || from >= 0 && plan.RegisterReserved(from, r) {
				m.add(r)
			}
		}
		return m
	}
	m = plan.ReservedRegions[region]
	if from >= 0 {
		other := plan.ReservedRegions[from]
		for w := range m {
			m[w] |= other[w]
		}
	}
	for w := range m {
		m[w] &= allocatable[w]
	}
	return m
}

// onlineHomeOperand returns the operand of a home store or load at home h, and
// whether the instruction has one: a store of a plain constant to a register
// home writes the constant itself.
func onlineHomeOperand(s *onlineSetup, h machine.Home, ins *machineInstruction) (regalloc.Operand, bool) {
	p := &s.program
	if ins.Op == machine.SelectedHomeGet {
		return regalloc.Operand{Value: ins.Dst, Def: true}, true
	}
	if h.Register < 0 {
		return regalloc.Operand{Value: ins.A}, true
	}
	if v := &p.Values[ins.A]; v.Constant && v.Symbol == "" {
		return regalloc.Operand{}, false
	}
	return regalloc.Operand{Value: ins.A, Fixed: regalloc.SingleRegister(physReg(h.Register))}, true
}

// emitOnlineHome emits what a home store or load at position, at home h,
// leaves to the walk, with its operand in g, or homes naming the home
// register a value is read from in place.
func emitOnlineHome(s *onlineSetup, h machine.Home, position int, ins *machineInstruction, g regalloc.Group, homes *onlineHomePlan) {
	p, emitter, written := &s.program, s.emitter, s.written
	t := s.temps
	part := ins.Variable*2 + int(ins.Imm)
	if ins.Op == machine.SelectedHomeSet && homes.inPlace(ins.A, position) >= 0 {
		// The value is another home's register, read in place.
		if src := physReg(homes.inPlace(ins.A, position)); h.Register >= 0 {
			if physReg(h.Register) != src {
				emitHomeMove(emitter, ins.Bits, physReg(h.Register), src)
				written[physReg(h.Register)] = true
			}
		} else {
			emitHomeSpill(emitter, src, h.Slot, ins.Bits)
		}
		t.setHome(part, ins.A)
		return
	}
	if ins.Op == machine.SelectedHomeGet && homes.loaded(ins.Dst, position) {
		g = regalloc.Group{} // it stays in its home
	}
	if ins.Op == machine.SelectedHomeSet {
		if h.Register >= 0 {
			home := physReg(h.Register)
			if v := &p.Values[ins.A]; v.Constant && v.Symbol == "" {
				if backend.IsVectorRegister(home) {
					emitter.(*physicalEmitter).literalValue(home, ins.Bits, v.Imm)
				} else {
					emitter.constant(home, v.Imm)
				}
			}
			written[home] = true // Online placed any other value there
		} else {
			for word := range int(g.Words) {
				emitHomeSpill(emitter, g.Registers[word], h.Slot+word, ins.Bits)
			}
		}
		t.setHome(part, ins.A)
		return
	}
	for word := range int(g.Words) {
		r := g.Registers[word]
		if h.Register >= 0 {
			emitHomeMove(emitter, ins.Bits, r, physReg(h.Register))
		} else {
			emitHomeReload(emitter, r, h.Slot+word, ins.Bits)
		}
		written[r] = true
	}
	if source := t.home(part); source != 0 {
		emitter.aliasFlagValue(ins.Dst, source-1, ins.Bits)
	}
}

// onlineTransition is one move of the homes at a scope boundary: a store of
// a home leaving its register, or a reload of one taking a new register.
type onlineTransition struct {
	register physReg
	slot     int
	bits     uint8
	reload   bool
}

// planTransition appends to ops the moves of the homes from region to next
// at a scope boundary, whose position reserves both regions' registers:
// first the stores of the homes that leave their register, then the
// reloads of those that take a new one, as transitionHomes makes them. It
// also reports whether the boundary moves a register home: a variable
// either sees with another register, or only one of them sees.
func planTransition(plan *machine.HomePlan, region, next int, ops []onlineTransition) ([]onlineTransition, bool) {
	moves := false
	for _, id := range onlineHomeIDs(plan, region) {
		h, exists := plan.Lookup(region, id)
		n, ok := plan.Lookup(next, id)
		moves = moves || !ok || n.Register != h.Register
		if exists && ok && h.Register >= 0 && h.Register != n.Register && (plan.Parents[region] != next || plan.Modified[region][id]) {
			ops = append(ops, onlineTransition{register: physReg(h.Register), slot: h.Slot, bits: h.Bits})
		}
	}
	for _, id := range onlineHomeIDs(plan, next) {
		h, exists := plan.Lookup(next, id)
		o, ok := plan.Lookup(region, id)
		moves = moves || !ok || o.Register != h.Register
		if exists && h.Register >= 0 && ok && o.Register != h.Register {
			ops = append(ops, onlineTransition{register: physReg(h.Register), slot: h.Slot, bits: h.Bits, reload: true})
		}
	}
	return ops, moves
}

// emitOnlineTransition emits boundary k's moves, as planOnlineHomes planned
// them.
func emitOnlineTransition(s *onlineSetup, k int) {
	t, emitter, written := s.temps, s.emitter, s.written
	for _, m := range t.onlineTransitions[t.onlineTransitionAt[k]:t.onlineTransitionAt[k+1]] {
		if m.reload {
			emitHomeReload(emitter, m.register, m.slot, m.bits)
			written[m.register] = true
		} else {
			emitHomeSpill(emitter, m.register, m.slot, m.bits)
		}
	}
}

// onlineHomeIDs returns the variables with a register home in region, in
// order.
func onlineHomeIDs(plan *machine.HomePlan, region int) []int {
	if plan.RegisterVariables != nil {
		return plan.RegisterVariables[region]
	}
	homes := plan.Regions
	if homes == nil {
		homes = plan.RegisterOverrides
	}
	var ids []int
	for id, home := range homes[region] {
		if home.Register >= 0 {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// onlineSegment is a run of positions [start, end) in one home region with
// one reserved set: a region's runs split where a home register starts to
// hold its variable.
type onlineSegment struct {
	start, end int
	region     int
	registers  homeMask
	moves      bool // at a boundary: whether it moves a register home
}

// onlineHomeDef asks the definition at position for a home register: Fixed
// when the home is reserved there, as a preference while it is not yet.
type onlineHomeDef struct {
	position int
	register physReg
	fixed    bool
}

// onlineHomePlan is what describeOnline decides about homes before it
// describes the positions.
type onlineHomePlan struct {
	segments []onlineSegment
	defs     []onlineHomeDef // by position
	// allocatable is Config.Registers.
	allocatable homeMask
	// snapshot is, per value, the home register its uses after position
	// from read in place, or -1. A load's value needs no register of its
	// own when every use reads the home before anything writes it, nor
	// does a stored value after the store, which the home then holds too.
	snapshot []int16
	from     []int32
}

// inPlace returns the home register value's use at position reads, or -1
// when the use reads the value's own register.
func (h *onlineHomePlan) inPlace(value, position int) int {
	if h.snapshot == nil || h.snapshot[value] < 0 || position <= int(h.from[value]) {
		return -1
	}
	return int(h.snapshot[value])
}

// loaded reports whether a load's value is read in place throughout.
func (h *onlineHomePlan) loaded(value, position int) bool {
	return h.snapshot != nil && h.snapshot[value] >= 0 && int(h.from[value]) == position
}

// planOnlineHomes plans the homes of s.program, with each value's last use
// as describeOnline keeps it.
func planOnlineHomes(s *onlineSetup, lastUse []int) onlineHomePlan {
	p, t := &s.program, s.temps
	plan := p.HomePlan
	last := len(p.Instructions)
	allocatable := registerMask(s.config.Registers)
	runs := onlineHomeRuns(s, t.onlineRuns[:0])
	t.onlineRuns = runs
	// Each home access's home, looked up once.
	homes := resetBufferTracked(&t.onlineHomeAt, &t.history.onlineHomeAt, last)
	bits := s.target.RegisterBits()
	for _, run := range runs {
		for pos := run.start; pos < min(run.end, last); pos++ {
			if ins := &p.Instructions[pos]; ins.Op == machine.SelectedHomeGet || ins.Op == machine.SelectedHomeSet {
				homes[pos] = homeAt(plan, run.region, ins, bits)
			}
		}
	}
	// A variable's home register holds nothing until its first store when
	// nothing reads it before: at the start of the segment, a variable whose
	// first access there is a store; after a boundary, one the previous
	// region did not see. Its register stays the allocator's until then.
	// The per-variable tables span the plan, which segments share: they stay
	// zero between uses, and each use clears the entries it set.
	marks := onlineVarTable(&t.onlineVarMarks, &t.history.onlineVarMarks, len(plan.Slots))
	segments := t.onlineSegments[:0]
	// Each boundary's moves, in order: boundary k's are
	// transitions[transitionAt[k]:transitionAt[k+1]].
	transitions := t.onlineTransitions[:0]
	transitionAt := append(t.onlineTransitionAt[:0], 0)
	type activation struct {
		position int
		register physReg
	}
	var pending []activation
	for k, run := range runs {
		full := homeRegisters(plan, allocatable, run.region, run.from)
		if run.from >= 0 {
			var moves bool
			transitions, moves = planTransition(plan, run.from, run.region, transitions)
			transitionAt = append(transitionAt, int32(len(transitions)))
			segments = append(segments, onlineSegment{run.start, run.end, run.region, full, moves})
			continue
		}
		if full.empty() {
			segments = append(segments, onlineSegment{run.start, run.end, run.region, full, false})
			continue
		}
		previous := -1
		if k > 0 && runs[k-1].from >= 0 {
			previous = runs[k-1].from
		}
		pending = pending[:0]
		for pos := run.start; pos < min(run.end, last); pos++ {
			ins := &p.Instructions[pos]
			if ins.Op != machine.SelectedHomeGet && ins.Op != machine.SelectedHomeSet || marks[ins.Variable] != 0 {
				continue
			}
			marks[ins.Variable] = 1 // first access seen
			if ins.Op != machine.SelectedHomeSet {
				continue
			}
			h := homes[pos]
			if h.Register < 0 || !full.contains(physReg(h.Register)) {
				continue
			}
			if k == 0 || previous >= 0 && !onlineVisible(plan, previous, ins.Variable) {
				pending = append(pending, activation{pos, physReg(h.Register)})
			}
		}
		for pos := run.start; pos < min(run.end, last); pos++ {
			if ins := &p.Instructions[pos]; ins.Op == machine.SelectedHomeGet || ins.Op == machine.SelectedHomeSet {
				marks[ins.Variable] = 0
			}
		}
		if len(pending) == 0 {
			segments = append(segments, onlineSegment{run.start, run.end, run.region, full, false})
			continue
		}
		// pending is in position order; each activation starts a segment.
		start, at := run.start, 0
		for {
			held := full
			for _, a := range pending[at:] {
				held[a.register>>6] &^= 1 << (a.register & 63)
			}
			end := run.end
			if at < len(pending) {
				end = pending[at].position
			}
			if start < end {
				segments = append(segments, onlineSegment{start, end, run.region, held, false})
			}
			if at == len(pending) {
				break
			}
			start = end
			for at < len(pending) && pending[at].position == start {
				at++
			}
		}
	}
	t.onlineSegments = segments
	t.onlineTransitions, t.onlineTransitionAt = transitions, transitionAt
	// A result stored to a register home at the next position is computed
	// there.
	defs := t.onlineHomeDefs[:0]
	seg := 0
	for pos := 0; pos+1 < last; pos++ {
		for segments[seg].end <= pos {
			seg++
		}
		ins, next := &p.Instructions[pos], &p.Instructions[pos+1]
		// A call's result arrives in its result register.
		if ins.Dst < 0 || next.Op != machine.SelectedHomeSet || next.A != ins.Dst || p.Values[ins.Dst].Constant ||
			ins.Op == machine.SelectedHomeGet || isCall(ins.Op) || onlineExpanded(s, pos) || max(int(p.Values[ins.Dst].Words), 1) != 1 {
			continue
		}
		h := homes[pos+1]
		if h.Register < 0 {
			continue
		}
		r := physReg(h.Register)
		if backend.IsVectorRegister(r) != (p.Values[ins.Dst].Class == backend.Float || p.Values[ins.Dst].Class == backend.Vector) {
			continue
		}
		defs = append(defs, onlineHomeDef{pos, r, segments[seg].registers.contains(r)})
	}
	t.onlineHomeDefs = defs
	// Loads and stored values read in place: scanning backward, kill[v] is
	// the next position that writes v's home (its store, or a definition
	// fixed to it) or moves homes (a boundary). A call saves and restores
	// the homes it overwrites, so values read in place survive it.
	snapshot := resetBufferTracked(&t.onlineSnapshot, &t.history.onlineSnapshot, len(p.Values))
	for i := range snapshot {
		snapshot[i] = -1
	}
	from := resetBuffer(&t.onlineSnapFrom, len(p.Values))
	// kills[v] is 1 + twice the position of v's next write, plus one when it
	// is a definition that reads its inputs before it writes; 0 for none.
	kills := onlineVarTable(&t.onlineKills, &t.history.onlineKills, len(plan.Slots))
	// readable reports whether the uses of v after position read the home
	// of variable before anything writes it: at an earlier position, at the
	// definition fixed to the home where its expansion may write the
	// destination over that input, or at the end when nothing writes the
	// home after. A store of v itself leaves the home holding v: the uses
	// up to it read the home, and so may those after it that read it in
	// place from there.
	readable := func(v, variable, position, boundary int) bool {
		if s.block != nil && t.onlineEscaping[v] {
			return false // an edge passes it: Online must hold it
		}
		u := lastUse[v]
		if u < 0 {
			u = -u
		}
		kill := kills[variable] - 1
		if kill < 0 {
			kill = 2 * last
		}
		used := u - 1
		switch {
		case u == 0 || used <= position:
			return false
		case used < kill/2 && used < boundary:
		case used == kill/2 && kill%2 == 1 && onlineReadsInPlace(s, used, v) && used < boundary:
		case used == last && kill == 2*last && boundary == last:
		case kill%2 == 0 && kill/2 < last && kill/2 < boundary && p.Instructions[kill/2].A == v &&
			(used == kill/2 || snapshot[v] >= 0 && int(from[v]) == kill/2):
			// kill/2 is a store to variable's home, of v.
		default:
			return false
		}
		return true
	}
	boundary := last
	d := len(defs) - 1
	seg = len(segments) - 1
	for pos := last - 1; pos >= 0; pos-- {
		for segments[seg].start > pos {
			seg--
		}
		for d >= 0 && defs[d].position >= pos {
			d--
		}
		ins := &p.Instructions[pos]
		switch ins.Op {
		case machine.SelectedHomeBoundary:
			// One that moves no home leaves every home register holding its
			// variable.
			if seg == 0 || segments[seg].moves {
				boundary = pos
			}
		default:
			// A 386 expansion reads its operands itself.
			if onlineExpanded(s, pos) {
				boundary = pos
			}
		case machine.SelectedHomeSet:
			// After the store, its value's uses may read the home, which
			// holds it until the next write.
			v := ins.A
			if h := homes[pos]; h.Register >= 0 && ins.Imm == 0 && !p.Values[v].Constant && max(int(p.Values[v].Words), 1) == 1 &&
				segments[seg].registers.contains(physReg(h.Register)) && readable(v, ins.Variable, pos, boundary) {
				snapshot[v], from[v] = int16(h.Register), int32(pos)
			}
			// A definition fixed to the home reads its inputs first.
			kills[ins.Variable] = 1 + 2*pos
			if d >= 0 && defs[d].position == pos-1 && defs[d].fixed {
				kills[ins.Variable] = 1 + 2*(pos-1) + 1
			}
		case machine.SelectedHomeGet:
			v := ins.Dst
			h := homes[pos]
			if h.Register < 0 || ins.Imm != 0 || p.Values[v].Constant || max(int(p.Values[v].Words), 1) != 1 ||
				!segments[seg].registers.contains(physReg(h.Register)) {
				continue
			}
			if readable(v, ins.Variable, pos, boundary) {
				snapshot[v], from[v] = int16(h.Register), int32(pos)
			}
		}
	}
	for pos := range p.Instructions {
		if ins := &p.Instructions[pos]; ins.Op == machine.SelectedHomeSet {
			kills[ins.Variable] = 0
		}
	}
	return onlineHomePlan{segments: segments, defs: defs, snapshot: snapshot, from: from, allocatable: allocatable}
}

// onlineReadsInPlace reports whether position's instruction may have its
// destination in the register of each input that is value.
func onlineReadsInPlace(s *onlineSetup, position, value int) bool {
	t := s.temps
	ins := &s.program.Instructions[position]
	inputs := t.onlineInputs[t.onlineInputStart[position]:t.onlineInputStart[position+1]]
	// As describeOnline's ties: a float result may also take any input
	// of an FP instruction, or the first on x86.
	x86 := s.target.Architecture() == "amd64" || s.target.Architecture() == "386"
	floatReuse := ins.Dst >= 0 && s.program.Values[ins.Dst].Class == backend.Float && (fpInstruction(*ins) || ins.Op == machine.SelectedFloatMove)
	found := false
	for i, id := range inputs {
		if id == value {
			if !(floatReuse && (!x86 || i == 0) || s.target.CanReuse(*ins, i)) {
				return false
			}
			found = true
		}
	}
	return found
}

// onlineVarTable returns *table with room for n entries, all zero, growing
// it zeroed; callers clear what they set.
func onlineVarTable[T int | uint8](table *[]T, h *machine.BufferHistory, n int) []T {
	h.Observe(n)
	if len(*table) < n {
		*table = make([]T, n)
	}
	return *table
}

// onlineExpanded reports whether the instruction at pos is a 386 expansion.
func onlineExpanded(s *onlineSetup, pos int) bool {
	return len(s.temps.online386) != 0 && s.temps.online386[pos] != o386Generic
}

// onlineVisible reports whether region sees variable.
func onlineVisible(plan *machine.HomePlan, region, variable int) bool {
	_, ok := plan.Lookup(region, variable)
	return ok
}
