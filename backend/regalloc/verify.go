package regalloc

import (
	"fmt"
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// Verify allocates f with a and checks the result. It walks f the way the
// Allocator contract describes, resolving each block's edges right after
// its last position, records the moves, and checks by abstract interpretation over every
// path that each use finds its value in the registers Operand reports, that
// fixed operands and abi locations are respected, and that ties, early
// clobbers, calls' clobbers, spills, reloads and edges keep values intact.
// It returns nil, the error a returns, or an error naming the first
// position, value and location that disagree.
func Verify(config Config, f Function, a Allocator) error {
	w, err := recordWalk(config, f, a)
	if err != nil {
		return err
	}
	v := newVerifier(config, f, w)
	return v.run()
}

// recordedKind is the kind of one Mover call.
type recordedKind uint8

const (
	recMove recordedKind = iota
	recSpill
	recReload
	recIncoming
	recRemat
)

// walkMove is one recorded Mover call.
type walkMove struct {
	kind     recordedKind
	value    int
	to, from Group
	slot     int
	offsets  [2]int
}

// walkRecord is one walk of an Allocator: the moves of each part and the
// operands' registers at each position.
type walkRecord struct {
	entry         []walkMove
	before, after [][]walkMove
	edges         [][]walkMove
	operands      [][]Group
	memory        [][]ValueLocation // per operand, for Memory uses
	edgeEmpty     []bool
	slots         int
}

// walkRecorder is the Mover a recorded walk passes to the Allocator.
type walkRecorder struct{ into *[]walkMove }

func (r *walkRecorder) add(m walkMove) { *r.into = append(*r.into, m) }

func (r *walkRecorder) Move(value int, to, from Group) {
	r.add(walkMove{kind: recMove, value: value, to: to, from: from})
}
func (r *walkRecorder) Spill(value int, slot int, from Group) {
	r.add(walkMove{kind: recSpill, value: value, from: from, slot: slot})
}
func (r *walkRecorder) Reload(value int, to Group, slot int) {
	r.add(walkMove{kind: recReload, value: value, to: to, slot: slot})
}
func (r *walkRecorder) LoadIncoming(value int, to Group, offsets [2]int) {
	r.add(walkMove{kind: recIncoming, value: value, to: to, offsets: offsets})
}
func (r *walkRecorder) Rematerialize(value int, to Group) error {
	r.add(walkMove{kind: recRemat, value: value, to: to})
	return nil
}

// recordWalk walks f with a, resolving each block's edges, in edge order,
// right after its last position.
func recordWalk(config Config, f Function, a Allocator) (*walkRecord, error) {
	w := &walkRecord{}
	r := &walkRecorder{into: &w.entry}
	if err := a.Allocate(config, f, r); err != nil {
		return nil, err
	}
	defer a.Finish()
	if err := a.Entry(); err != nil {
		return nil, err
	}
	n := f.Positions()
	w.edges, w.edgeEmpty = make([][]walkMove, f.Edges()), make([]bool, f.Edges())
	ends := make([][]int, n) // the edges resolved after each position
	for e := range f.Edges() {
		last := f.Block(f.Edge(e).From).End - 1
		ends[last] = append(ends[last], e)
	}
	w.before, w.after, w.operands = make([][]walkMove, n), make([][]walkMove, n), make([][]Group, n)
	w.memory = make([][]ValueLocation, n)
	var ops []Operand
	for p := range n {
		r.into = &w.before[p]
		if err := a.Before(p); err != nil {
			return nil, err
		}
		ops = f.Operands(p, ops[:0])
		w.operands[p] = make([]Group, len(ops))
		w.memory[p] = make([]ValueLocation, len(ops))
		for i, o := range ops {
			w.operands[p][i] = a.Operand(p, i)
			if o.Memory {
				w.memory[p][i] = a.Memory(p, i)
			}
		}
		r.into = &w.after[p]
		if err := a.After(p); err != nil {
			return nil, err
		}
		for _, e := range ends[p] {
			r.into = &w.edges[e]
			if err := a.Edge(e); err != nil {
				return nil, err
			}
			w.edgeEmpty[e] = a.EdgeEmpty(e)
		}
	}
	w.slots = a.Slots()
	return w, nil
}

// replay sends recorded moves to m.
func (w *walkRecord) replay(moves []walkMove, m Mover) error {
	for _, x := range moves {
		switch x.kind {
		case recMove:
			m.Move(x.value, x.to, x.from)
		case recSpill:
			m.Spill(x.value, x.slot, x.from)
		case recReload:
			m.Reload(x.value, x.to, x.slot)
		case recIncoming:
			m.LoadIncoming(x.value, x.to, x.offsets)
		case recRemat:
			if err := m.Rematerialize(x.value, x.to); err != nil {
				return err
			}
		}
	}
	return nil
}

// A tag names one word of a value a location holds: value*4 + word. Tags at
// or above oldBase name an edge destination's parameters as they were
// before the edge.
type tag = int32

// cells is what each location holds: the tags of the values whose words it
// equals. Registers are cells [0, 256), frame slots follow.
type cells [][]tag

func (c cells) clone() cells {
	out := make(cells, len(c))
	for i, s := range c {
		out[i] = slices.Clone(s)
	}
	return out
}

// meet keeps in c what c and d agree on and reports whether c changed.
func (c cells) meet(d cells) bool {
	changed := false
	for i := range c {
		kept := c[i][:0]
		for _, t := range c[i] {
			if slices.Contains(d[i], t) {
				kept = append(kept, t)
			}
		}
		changed = changed || len(kept) != len(c[i])
		c[i] = kept
	}
	return changed
}

type verifier struct {
	config   Config
	f        Function
	w        *walkRecord
	values   []Value
	oldBase  tag
	incoming map[int]tag
	named    map[string]backend.Register
	// edgeParams and edgeArgs alias an edge's parameters to its arguments
	// while its moves are checked.
	edgeParams, edgeArgs []int
	checking             bool
	err                  error
	ops                  []Operand
	// scopes are the reserved registers. Moves must not write forbid but
	// for allow, the registers of the position's constrained operands.
	scopes        scopeMap
	forbid, allow Pins
}

func newVerifier(config Config, f Function, w *walkRecord) *verifier {
	v := &verifier{config: config, f: f, w: w, incoming: map[int]tag{}, named: map[string]backend.Register{}}
	v.values = make([]Value, f.Values())
	for i := range v.values {
		v.values[i] = f.Value(i)
	}
	v.oldBase = tag(len(v.values)) * 4
	if err := readScopes(f, f.Positions(), config.Registers, &v.scopes); err != nil {
		v.err = err
	}
	for r := range config.Registers.All() {
		if name := config.Registers.NameOf(r); name != "" {
			v.named[name] = r
		}
	}
	return v
}

func (v *verifier) words(value int) int {
	x := v.values[value]
	if x.Class == backend.Float || x.Words == 0 {
		return 1
	}
	return int(x.Words)
}

func (v *verifier) storage(value int) int {
	if x := v.values[value]; x.StorageWords != 0 {
		return int(x.StorageWords)
	}
	return v.words(value)
}

func valueTag(value, word int) tag { return tag(value)*4 + tag(word) }

func (v *verifier) fail(format string, args ...any) {
	if v.checking && v.err == nil {
		v.err = fmt.Errorf("regalloc: verify: "+format, args...)
	}
}

// holds reports whether cell holds word of value, counting an edge's
// parameter as its argument and a destination parameter as its value
// before the edge.
func (v *verifier) holds(cell []tag, value, word int) bool {
	if slices.Contains(cell, valueTag(value, word)) {
		return true
	}
	for i, p := range v.edgeParams {
		if p == value {
			if slices.Contains(cell, v.argTag(v.edgeArgs[i], word)) || slices.Contains(cell, v.oldBase+valueTag(p, word)) {
				return true
			}
		}
	}
	return false
}

// argTag is the tag an edge argument's word has during the edge: a
// destination parameter's own value is its value before the edge.
func (v *verifier) argTag(arg, word int) tag {
	if slices.Contains(v.edgeParams, arg) {
		return v.oldBase + valueTag(arg, word)
	}
	return valueTag(arg, word)
}

func (v *verifier) slotCell(s cells, slot int) int {
	if slot < 0 || 256+slot >= len(s) {
		v.fail("slot %d outside the %d-slot frame", slot, v.w.slots)
		return -1
	}
	return 256 + slot
}

func (v *verifier) checkGroup(g Group, where string) bool {
	if g.Words > 2 || g.Words == 2 && g.Registers[0] == g.Registers[1] {
		v.fail("%s: malformed group %v", where, g)
		return false
	}
	for i := range int(g.Words) {
		if !v.config.Registers.Contains(g.Registers[i]) {
			v.fail("%s: register %d is not allocatable", where, g.Registers[i])
			return false
		}
	}
	return true
}

// writes checks that a move into g leaves the client's registers alone.
func (v *verifier) writes(g Group, where string) {
	for i := range int(g.Words) {
		if r := g.Registers[i]; v.forbid.Contains(r) && !v.allow.Contains(r) {
			v.fail("%s: a move writes register %d, which is reserved", where, r)
		}
	}
}

// apply runs moves on s, checking each source.
func (v *verifier) apply(s cells, moves []walkMove, where string) {
	for _, m := range moves {
		if m.value < 0 || m.value >= len(v.values) {
			v.fail("%s: move of unknown value %d", where, m.value)
			continue
		}
		storage := v.storage(m.value)
		if m.kind != recSpill {
			v.writes(m.to, where)
		}
		switch m.kind {
		case recMove:
			if !v.checkGroup(m.to, where) || !v.checkGroup(m.from, where) || m.to.Words != m.from.Words {
				v.fail("%s: move of value %d between %v and %v", where, m.value, m.from, m.to)
				continue
			}
			if groupsOverlap(m.to, m.from) {
				v.fail("%s: move of value %d between overlapping %v and %v", where, m.value, m.from, m.to)
				continue
			}
			var src [2][]tag
			for k := range int(m.from.Words) {
				if !v.holds(s[m.from.Registers[k]], m.value, k) {
					v.fail("%s: move reads value %d word %d from register %d, which holds %v", where, m.value, k, m.from.Registers[k], s[m.from.Registers[k]])
				}
				src[k] = slices.Clone(s[m.from.Registers[k]])
			}
			for k := range int(m.to.Words) {
				s[m.to.Registers[k]] = src[k]
			}
		case recSpill:
			if !v.checkGroup(m.from, where) || m.from.Words == 0 {
				continue
			}
			for k := range int(m.from.Words) {
				if !v.holds(s[m.from.Registers[k]], m.value, k) {
					v.fail("%s: spill of value %d reads register %d, which holds %v", where, m.value, m.from.Registers[k], s[m.from.Registers[k]])
				}
			}
			for k := range storage {
				c := v.slotCell(s, m.slot+k)
				if c < 0 {
					break
				}
				if int(m.from.Words) == storage {
					s[c] = slices.Clone(s[m.from.Registers[k]])
				} else {
					s[c] = rewordTags(s[m.from.Registers[0]], 0, k)
				}
			}
		case recReload:
			if !v.checkGroup(m.to, where) || m.to.Words == 0 {
				continue
			}
			var loaded [2][]tag
			for k := range storage {
				c := v.slotCell(s, m.slot+k)
				if c < 0 {
					break
				}
				if !v.holds(s[c], m.value, k) {
					v.fail("%s: reload of value %d reads slot %d, which holds %v", where, m.value, m.slot+k, s[c])
				}
				switch {
				case int(m.to.Words) == storage:
					loaded[k] = slices.Clone(s[c])
				case k == 0:
					loaded[0] = rewordTags(s[c], 0, 0)
				default:
					loaded[0] = intersectTags(loaded[0], rewordTags(s[c], k, 0))
				}
			}
			for k := range int(m.to.Words) {
				s[m.to.Registers[k]] = loaded[k]
			}
		case recIncoming:
			if !v.checkGroup(m.to, where) {
				continue
			}
			for k := range int(m.to.Words) {
				t, ok := v.incoming[m.offsets[k]]
				if !ok || !v.holds([]tag{t}, m.value, k) {
					v.fail("%s: value %d word %d loaded from incoming offset %d", where, m.value, k, m.offsets[k])
				}
				s[m.to.Registers[k]] = nil
				if ok {
					s[m.to.Registers[k]] = []tag{t}
				}
			}
		case recRemat:
			if !v.checkGroup(m.to, where) {
				continue
			}
			if !v.constant(m.value) {
				v.fail("%s: rematerializes value %d, which is not a constant", where, m.value)
			}
			for k := range int(m.to.Words) {
				s[m.to.Registers[k]] = []tag{valueTag(m.value, k)}
			}
		}
	}
}

// constant reports whether value is a Constant, or an edge parameter whose
// argument is.
func (v *verifier) constant(value int) bool {
	if v.values[value].Constant {
		return true
	}
	for i, p := range v.edgeParams {
		if p == value && v.values[v.edgeArgs[i]].Constant {
			return true
		}
	}
	return false
}

// rewordTags returns the tags in cell for word from, renamed to word to.
func rewordTags(cell []tag, from, to int) []tag {
	var out []tag
	for _, t := range cell {
		if int(t%4) == from {
			out = append(out, t-tag(from)+tag(to))
		}
	}
	return out
}

func intersectTags(a, b []tag) []tag {
	var out []tag
	for _, t := range a {
		if slices.Contains(b, t) {
			out = append(out, t)
		}
	}
	return out
}

// forget removes every word of value from s: it is being redefined.
func forget(s cells, value int) {
	for i := range s {
		s[i] = slices.DeleteFunc(s[i], func(t tag) bool { return int(t/4) == value })
	}
}

// registersNamed resolves an abi location's registers, or returns false for
// a location with stack words.
func (v *verifier) registersNamed(loc abi.ValueLocation) (Group, bool) {
	g := Group{Words: uint8(len(loc.Words))}
	if len(loc.Words) == 0 || len(loc.Words) > 2 {
		return Group{}, false
	}
	for i, word := range loc.Words {
		if word.Storage != abi.Register {
			return Group{}, false
		}
		r, ok := v.named[word.Register]
		if !ok {
			return Group{}, false
		}
		g.Registers[i] = r
	}
	return g, true
}

// position runs position p on s.
func (v *verifier) position(s cells, p int) {
	where := fmt.Sprintf("position %d", p)
	v.ops = v.f.Operands(p, v.ops[:0])
	groups := v.w.operands[p]
	if len(groups) != len(v.ops) {
		v.fail("%s: %d operand groups for %d operands", where, len(groups), len(v.ops))
		return
	}
	effect := v.f.Call(p)
	returns := v.f.Returns(p)
	// The operands fixed by the function or abi may use reserved registers,
	// as may a definition tied to one of them.
	reserved := v.scopes.reserved(p)
	var constrained []bool
	{
		use, def := 0, 0
		constrained = make([]bool, len(v.ops))
		for i, o := range v.ops {
			c := o.Fixed.Words != 0
			if o.Def {
				c = c || effect != nil && def == 0 && len(effect.ResultLocation.Words) != 0
				def++
			} else {
				c = c || effect != nil && use < len(effect.ArgumentLocations) && len(effect.ArgumentLocations[use].Words) != 0 &&
					effect.ArgumentLocations[use].Words[0].Storage == abi.Register
				c = c || returns && use == 0
				use++
			}
			constrained[i] = c
		}
		for i, o := range v.ops {
			if o.Def && o.Tied && o.TiedUse >= 0 && o.TiedUse < len(v.ops) && constrained[o.TiedUse] {
				constrained[i] = true
			}
		}
	}
	v.forbid, v.allow = reserved, Pins{}
	for i, g := range groups {
		if constrained[i] {
			v.allow.Add(g)
			continue
		}
		if !groupsOverlapPins(g, reserved) {
			continue
		}
		// A value fixed in a reserved register may be read there again.
		shared := false
		for j, o := range v.ops {
			shared = shared || constrained[j] && !o.Def && !v.ops[i].Def && o.Value == v.ops[i].Value && groups[j] == g
		}
		if !shared {
			v.fail("%s: operand %d (value %d) in %v, which is reserved", where, i, v.ops[i].Value, g)
		}
	}
	v.apply(s, v.w.before[p], where+" before")
	use, def := 0, 0
	for i, o := range v.ops {
		g := groups[i]
		if !v.checkGroup(g, where) {
			continue
		}
		if o.Def {
			if v.values[o.Value].Constant {
				if g.Words != 0 {
					v.fail("%s: constant definition %d in %v", where, o.Value, g)
				}
				def++
				continue
			}
			if int(g.Words) != v.words(o.Value) {
				v.fail("%s: definition %d in %v, want %d words", where, o.Value, g, v.words(o.Value))
			}
			if o.Fixed.Words != 0 && g != o.Fixed {
				v.fail("%s: definition %d in %v, fixed to %v", where, o.Value, g, o.Fixed)
			}
			if effect != nil && def == 0 && len(effect.ResultLocation.Words) != 0 {
				if want, ok := v.registersNamed(effect.ResultLocation); ok && g != want {
					v.fail("%s: call result %d in %v, want %v", where, o.Value, g, want)
				}
			}
			if o.Tied && o.TiedUse >= 0 && o.TiedUse < len(v.ops) && (o.Prefer.Words == 0 || g != o.Prefer) {
				tied := v.ops[o.TiedUse].Value
				for k := range int(g.Words) {
					if !v.holds(s[g.Registers[k]], tied, k) {
						v.fail("%s: tied definition %d in %v does not hold use %d", where, o.Value, g, tied)
						break
					}
				}
			}
			// A tied definition that took Prefer avoids the uses as an
			// early clobber does.
			if o.EarlyClobber || o.Tied && o.Prefer.Words != 0 && g == o.Prefer {
				for j, other := range v.ops {
					if !other.Def && groupsOverlap(g, groups[j]) {
						v.fail("%s: early-clobber definition %d in %v overlaps use %d in %v", where, o.Value, g, other.Value, groups[j])
					}
				}
			}
			for j := range i {
				if v.ops[j].Def && groupsOverlap(g, groups[j]) {
					v.fail("%s: definitions %d and %d share registers", where, v.ops[j].Value, o.Value)
				}
			}
			def++
			continue
		}
		if o.Memory {
			v.memoryUse(s, o.Value, g, v.w.memory[p][i], where)
			use++
			continue
		}
		if int(g.Words) != v.words(o.Value) {
			v.fail("%s: use %d in %v, want %d words", where, o.Value, g, v.words(o.Value))
		}
		for k := range int(g.Words) {
			if !v.holds(s[g.Registers[k]], o.Value, k) {
				v.fail("%s: use %d word %d in register %d, which holds %v", where, o.Value, k, g.Registers[k], s[g.Registers[k]])
			}
		}
		if o.Fixed.Words != 0 && g != o.Fixed {
			v.fail("%s: use %d in %v, fixed to %v", where, o.Value, g, o.Fixed)
		}
		if effect != nil && use < len(effect.ArgumentLocations) {
			if want, ok := v.registersNamed(effect.ArgumentLocations[use]); ok && g != want {
				v.fail("%s: call argument %d in %v, want %v", where, o.Value, g, want)
			}
		}
		if returns && use == 0 {
			if want, ok := v.registersNamed(v.f.Layout().Result); ok && g != want {
				v.fail("%s: returned value %d in %v, want %v", where, o.Value, g, want)
			}
		}
		use++
	}
	// The client may use its reserved registers during the instruction, and
	// the instruction overwrites its clobbers.
	clobbers := v.f.Clobbers(p)
	for r := range s[:256] {
		if reserved.Contains(backend.Register(r)) || clobbers.Contains(backend.Register(r)) {
			s[r] = nil
		}
	}
	if effect != nil {
		for _, names := range [][]string{effect.ClobberedRegisters, effect.ClobberedFPRegisters} {
			for _, name := range names {
				if r, ok := v.named[name]; ok {
					s[r] = nil
				}
			}
		}
	}
	for i, o := range v.ops {
		if !o.Def || v.values[o.Value].Constant {
			continue
		}
		forget(s, o.Value)
		g := groups[i]
		for k := range int(g.Words) {
			s[g.Registers[k]] = []tag{valueTag(o.Value, k)}
		}
	}
	v.allow = Pins{}
	v.apply(s, v.w.after[p], where+" after")
	v.forbid = Pins{}
}

// edge returns the state at the start of edge e's destination, from s at
// the end of its source.
func (v *verifier) edge(s cells, e int) cells {
	edge := v.f.Edge(e)
	t := s.clone()
	params := v.f.Block(edge.To).Params
	// The destination's parameters as they were become old tags.
	for i := range t {
		for j, x := range t[i] {
			if int(x) < int(v.oldBase) && slices.Contains(params, int(x/4)) {
				t[i][j] = v.oldBase + x
			}
		}
	}
	v.edgeParams, v.edgeArgs = params, edge.Args
	v.forbid, v.allow = v.scopes.reserved(v.f.Block(edge.From).End-1), Pins{}
	for w, bits := range v.scopes.reserved(v.f.Block(edge.To).Start).bits {
		v.forbid.bits[w] |= bits
	}
	v.apply(t, v.w.edges[e], fmt.Sprintf("edge %d", e))
	v.forbid = Pins{}
	// Each parameter is where its argument is, or where the moves put it.
	for i := range t {
		var add []tag
		for k, p := range params {
			if k >= len(edge.Args) {
				break
			}
			for word := range 4 {
				if slices.Contains(t[i], v.argTag(edge.Args[k], word)) {
					add = append(add, valueTag(p, word))
				}
			}
		}
		for _, x := range add {
			if !slices.Contains(t[i], x) {
				t[i] = append(t[i], x)
			}
		}
		t[i] = slices.DeleteFunc(t[i], func(x tag) bool { return x >= v.oldBase })
	}
	v.edgeParams, v.edgeArgs = nil, nil
	return t
}

// memoryUse checks that a use read from memory is where loc says.
func (v *verifier) memoryUse(s cells, value int, g Group, loc ValueLocation, where string) {
	if g.Words != 0 {
		v.fail("%s: use %d read from memory has registers %v", where, value, g)
	}
	switch {
	case loc.Slot >= 0:
		if int(loc.SlotWords) != v.storage(value) {
			v.fail("%s: use %d read from %d slot words, want %d", where, value, loc.SlotWords, v.storage(value))
		}
		for k := range v.storage(value) {
			if c := v.slotCell(s, loc.Slot+k); c >= 0 && !v.holds(s[c], value, k) {
				v.fail("%s: use %d word %d read from slot %d, which holds %v", where, value, k, loc.Slot+k, s[c])
			}
		}
	case loc.IncomingWords != 0:
		for k := range int(loc.IncomingWords) {
			t, ok := v.incoming[loc.Incoming[k]]
			if !ok || !v.holds([]tag{t}, value, k) {
				v.fail("%s: use %d word %d read from incoming offset %d", where, value, k, loc.Incoming[k])
			}
		}
	case !v.values[value].Constant:
		v.fail("%s: use %d read from memory has no location", where, value)
	}
}

func groupsOverlap(a, b Group) bool {
	for i := range int(a.Words) {
		if b.Contains(a.Registers[i]) {
			return true
		}
	}
	return false
}

// run finds the state at every block's start by iterating to a fixed point,
// then checks every reachable block once.
func (v *verifier) run() error {
	f := v.f
	blocks := f.Blocks()
	if blocks == 0 || len(v.w.operands) != f.Positions() {
		return fmt.Errorf("regalloc: verify: walk does not cover the function")
	}
	entry := make(cells, 256+v.w.slots)
	layout := f.Layout()
	params := f.Block(0).Params
	for i, x := range params {
		if i >= len(layout.Arguments) {
			break
		}
		for k, word := range layout.Arguments[i].Words {
			switch word.Storage {
			case abi.Register:
				if r, ok := v.named[word.Register]; ok {
					entry[r] = append(entry[r], valueTag(x, k))
				}
			case abi.Stack:
				v.incoming[word.StackOffset] = valueTag(x, k)
			case abi.Frame:
				if word.Slot >= 0 && 256+word.Slot < len(entry) {
					entry[256+word.Slot] = append(entry[256+word.Slot], valueTag(x, k))
				}
			}
		}
	}
	if v.err != nil {
		return v.err
	}
	v.checking = true
	v.forbid = v.scopes.reserved(0)
	v.apply(entry, v.w.entry, "entry")
	v.forbid = Pins{}
	v.checking = false
	if v.err != nil {
		return v.err
	}
	succs := make([][]int, blocks)
	for e := range f.Edges() {
		from := f.Edge(e).From
		succs[from] = append(succs[from], e)
	}
	in := make([]cells, blocks)
	in[0] = entry
	for changed := true; changed; {
		changed = false
		for b := range blocks {
			if in[b] == nil {
				continue
			}
			s := in[b].clone()
			block := f.Block(b)
			for p := block.Start; p < block.End; p++ {
				v.position(s, p)
			}
			for _, e := range succs[b] {
				t := v.edge(s, e)
				to := f.Edge(e).To
				switch {
				case in[to] == nil:
					in[to], changed = t, true
				case in[to].meet(t):
					changed = true
				}
			}
		}
	}
	v.checking = true
	for b := range blocks {
		if in[b] == nil {
			continue
		}
		s := in[b].clone()
		block := f.Block(b)
		for p := block.Start; p < block.End && v.err == nil; p++ {
			v.position(s, p)
		}
		for _, e := range succs[b] {
			v.edge(s, e)
		}
		if v.err != nil {
			return v.err
		}
	}
	return nil
}
