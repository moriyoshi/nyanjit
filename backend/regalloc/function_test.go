package regalloc

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// genFunction is a randomly generated valid Function with the facts a
// simulator needs to run it.
type genFunction struct {
	config    Config
	values    []Value
	constants map[int]uint64
	blocks    []Block
	edges     []Edge
	ops       [][]Operand
	calls     map[int]*abi.CallEffect
	returns   map[int]bool
	layout    abi.EntryLayout
	scopes    []Scope
	clobbers  map[int]backend.RegisterSet // positions overwriting registers
	succs     [][]int                     // edge indices per block
}

// function describes g through a FunctionBuilder.
func (g *genFunction) function() Function {
	f := buildFunction(g.values, g.blocks, g.edges, g.ops, g.calls, g.returns, g.layout).(*FunctionBuilder)
	for _, scope := range g.scopes {
		f.AddScope(scope)
	}
	for p, set := range g.clobbers {
		f.SetClobbers(p, set)
	}
	return f
}

// buildFunction describes a function with a FunctionBuilder; blocks must
// cover ops in order.
func buildFunction(values []Value, blocks []Block, edges []Edge, ops [][]Operand, calls map[int]*abi.CallEffect, returns map[int]bool, layout abi.EntryLayout) Function {
	b := new(FunctionBuilder)
	for _, v := range values {
		b.AddValue(v)
	}
	for _, block := range blocks {
		b.AddBlock(block.Params...)
		for p := block.Start; p < block.End; p++ {
			switch {
			case calls[p] != nil:
				b.AddCall(calls[p], ops[p]...)
			case returns[p]:
				b.AddReturn(ops[p]...)
			default:
				b.AddPosition(ops[p]...)
			}
		}
	}
	for _, e := range edges {
		b.AddEdge(e.From, e.To, e.Args...)
	}
	b.SetLayout(layout)
	return b
}

func (g *genFunction) words(v int) int {
	if g.values[v].Class == backend.Float || g.values[v].Words == 0 {
		return 1
	}
	return int(g.values[v].Words)
}

func regLocation(set backend.RegisterSet, group Group) abi.ValueLocation {
	var loc abi.ValueLocation
	for i := 0; i < int(group.Words); i++ {
		loc.Words = append(loc.Words, abi.WordLocation{Storage: abi.Register, Register: set.NameOf(group.Registers[i])})
	}
	return loc
}

// reservedAt returns the registers the innermost scope covering position
// reserves.
func (g *genFunction) reservedAt(position int) backend.RegisterSet {
	inner, best := -1, -1
	for i, scope := range g.scopes {
		depth := 0
		for q := scope.Parent; q >= 0; q = g.scopes[q].Parent {
			depth++
		}
		if scope.Start <= position && position < scope.End && depth > best {
			inner, best = i, depth
		}
	}
	if inner < 0 {
		return backend.RegisterSet{}
	}
	return g.scopes[inner].Registers
}

// single returns a one-word group at index in the function's registers.
func (g *genFunction) single(index int) Group { return SingleRegister(g.config.Registers.At(index)) }

// mask returns the indices of group's registers as a bitset.
func (g *genFunction) mask(group Group) uint64 {
	var m uint64
	for i := 0; i < int(group.Words); i++ {
		m |= 1 << uint(g.config.Registers.Index(group.Registers[i]))
	}
	return m
}

// generate builds a random function. Every block after the entry has a
// predecessor earlier in layout order, the last block returns, and every
// other block has a successor later in layout order.
func generate(seed uint64) *genFunction {
	rng := rand.New(rand.NewPCG(seed, 0x5eed))
	g := &genFunction{constants: map[int]uint64{}, calls: map[int]*abi.CallEffect{}, returns: map[int]bool{}}
	registers := 4 + rng.IntN(7)
	c := Config{SlotLimit: 1 << 20, ReservedSlots: rng.IntN(2), VectorSlotParity: rng.IntN(2)}
	var vectorMask, blockedMask uint64
	var regNames []string
	for i := range registers {
		regNames = append(regNames, fmt.Sprintf("r%d", i))
	}
	vectors := registers >= 6 && rng.IntN(3) == 0
	if vectors {
		vectorMask = 7 << uint(registers-3)
	}
	if rng.IntN(2) == 0 {
		blockedMask |= 1 << 1 // a scratch register
	}
	if vectors && rng.IntN(2) == 0 {
		blockedMask |= 1 << uint(registers-1)
	}
	c.Registers = named(testRegisters(registers, vectorMask), regNames)
	c.Blocked = testSet(c.Registers, blockedMask)
	c.Scratch = c.Blocked
	pairs := registers >= 7 && rng.IntN(4) == 0
	g.config = c
	general := func() []int {
		var out []int
		for i := range registers {
			if blockedMask&(1<<uint(i)) == 0 && vectorMask&(1<<uint(i)) == 0 {
				out = append(out, i)
			}
		}
		return out
	}()
	vectorRegs := func() []int {
		var out []int
		for i := range registers {
			if blockedMask&(1<<uint(i)) == 0 && vectorMask&(1<<uint(i)) != 0 {
				out = append(out, i)
			}
		}
		return out
	}()
	newValue := func(class backend.Class, words uint8, constant bool) int {
		v := Value{Class: class, Words: words, Constant: constant}
		if class == backend.Vector {
			v.StorageWords = 2
		}
		g.values = append(g.values, v)
		return len(g.values) - 1
	}
	randomValue := func() int {
		switch {
		case vectors && len(vectorRegs) != 0 && rng.IntN(4) == 0:
			return newValue(backend.Vector, 1, false)
		case pairs && len(general) >= 3 && rng.IntN(3) == 0:
			return newValue(backend.General, 2, false)
		}
		return newValue(backend.General, 1, false)
	}
	// Control flow.
	blocks := 1 + rng.IntN(8)
	g.succs = make([][]int, blocks)
	preds := make([][]int, blocks)
	addEdge := func(from, to int) {
		g.edges = append(g.edges, Edge{From: from, To: to})
		g.succs[from] = append(g.succs[from], len(g.edges)-1)
		preds[to] = append(preds[to], from)
	}
	for b := 0; b < blocks-1; b++ {
		addEdge(b, b+1+rng.IntN(blocks-1-b))
		if rng.IntN(2) == 0 {
			addEdge(b, 1+rng.IntN(blocks-1))
		}
	}
	for b := 1; b < blocks; b++ {
		if !slices.ContainsFunc(preds[b], func(p int) bool { return p < b }) {
			addEdge(b-1, b)
		}
	}
	dominators := dominatorSets(blocks, preds)
	// Block parameters.
	params := make([][]int, blocks)
	for b := 1; b < blocks; b++ {
		for range rng.IntN(3) {
			params[b] = append(params[b], randomValue())
		}
	}
	for range rng.IntN(4) {
		params[0] = append(params[0], randomValue())
	}
	// Entry layout: registers, incoming stack words or reserved frame slots.
	usedEntry := map[int]bool{}
	offset := 8
	for _, x := range params[0] {
		pool := general
		if g.values[x].Class == backend.Vector {
			pool = vectorRegs
		} else if blockedMask&(1<<1) != 0 && rng.IntN(3) == 0 {
			pool = append([]int{1}, general...) // arrives in a blocked register
		}
		var free []int
		for _, r := range pool {
			if !usedEntry[r] {
				free = append(free, r)
			}
		}
		words := g.words(x)
		if len(free) >= words && rng.IntN(3) != 0 {
			group := g.single(free[0])
			if words == 2 {
				group, _ = PairedRegisters(c.Registers.At(free[0]), c.Registers.At(free[1]))
			}
			for i := 0; i < words; i++ {
				usedEntry[free[i]] = true
			}
			g.layout.Arguments = append(g.layout.Arguments, regLocation(c.Registers, group))
			continue
		}
		var loc abi.ValueLocation
		if g.values[x].Class != backend.Vector && rng.IntN(3) == 0 {
			for range words {
				loc.Words = append(loc.Words, abi.WordLocation{Storage: abi.Frame, Slot: g.config.ReservedSlots})
				g.config.ReservedSlots++
			}
			g.layout.Arguments = append(g.layout.Arguments, loc)
			continue
		}
		for range words {
			loc.Words = append(loc.Words, abi.WordLocation{Storage: abi.Stack, StackOffset: offset})
			offset += 8
		}
		g.layout.Arguments = append(g.layout.Arguments, loc)
	}
	g.layout.Result = regLocation(c.Registers, g.single(general[0]))
	// Contents in layout order; values of dominating blocks are available.
	defined := make([][]int, blocks)
	position := 0
	for b := range blocks {
		var available []int
		for d := range blocks {
			if d != b && dominators[b][d] {
				available = append(available, params[d]...)
				available = append(available, defined[d]...)
			}
		}
		available = append(available, params[b]...)
		start := position
		emit := func(ops []Operand) {
			g.ops = append(g.ops, ops)
			for _, op := range ops {
				if op.Def {
					available = append(available, op.Value)
					defined[b] = append(defined[b], op.Value)
				}
			}
			position++
		}
		pick := func(match func(int) bool) int {
			var choices []int
			for _, v := range available {
				if match(v) {
					choices = append(choices, v)
				}
			}
			if len(choices) == 0 {
				return -1
			}
			return choices[rng.IntN(len(choices))]
		}
		single := func(v int) bool { return g.values[v].Class == backend.General && g.words(v) == 1 }
		constant := func() int {
			v := newValue(backend.General, 1, true)
			g.constants[v] = rng.Uint64()
			emit([]Operand{{Value: v, Def: true}})
			return v
		}
		for range 1 + rng.IntN(5) {
			switch kind := rng.IntN(8); {
			case kind == 0:
				constant()
			case kind == 1 && len(general) >= 3:
				// A call: arguments and result in abi registers.
				effect := &abi.CallEffect{}
				var ops []Operand
				argRegs := slices.Clone(general)
				rng.Shuffle(len(argRegs), func(i, j int) { argRegs[i], argRegs[j] = argRegs[j], argRegs[i] })
				for i := range rng.IntN(3) {
					v := pick(single)
					if v < 0 {
						break
					}
					ops = append(ops, Operand{Value: v})
					if rng.IntN(4) == 0 {
						effect.ArgumentLocations = append(effect.ArgumentLocations, abi.ValueLocation{Words: []abi.WordLocation{{Storage: abi.Stack, StackOffset: 8 * i}}})
						continue
					}
					effect.ArgumentLocations = append(effect.ArgumentLocations, regLocation(c.Registers, g.single(argRegs[i])))
				}
				for _, r := range general {
					if rng.IntN(2) == 0 {
						effect.ClobberedRegisters = append(effect.ClobberedRegisters, regNames[r])
					}
				}
				if rng.IntN(2) == 0 {
					result := general[rng.IntN(len(general))]
					effect.ResultLocation = regLocation(c.Registers, g.single(result))
					if !slices.Contains(effect.ClobberedRegisters, regNames[result]) {
						effect.ClobberedRegisters = append(effect.ClobberedRegisters, regNames[result])
					}
					ops = append(ops, Operand{Value: newValue(backend.General, 1, false), Def: true})
				}
				g.calls[position] = effect
				emit(ops)
			default:
				var ops []Operand
				fixedUsed := uint64(0)
				for range rng.IntN(max(1, min(4, len(general)-2))) {
					v := pick(func(int) bool { return true })
					if v < 0 {
						break
					}
					op := Operand{Value: v}
					if g.values[v].Class == backend.General && g.words(v) == 1 && rng.IntN(5) == 0 {
						r := general[rng.IntN(len(general))]
						taken := fixedUsed&(1<<uint(r)) != 0
						for _, other := range ops {
							taken = taken || other.Fixed.Words != 0 && other.Value == v
						}
						if !taken {
							op.Fixed = g.single(r)
							fixedUsed |= 1 << uint(r)
						}
					}
					ops = append(ops, op)
				}
				uses := len(ops)
				fixedDefs := uint64(0)
				for range rng.IntN(3) {
					v := randomValue()
					op := Operand{Value: v, Def: true}
					switch rng.IntN(5) {
					case 0:
						var candidates []int
						for i := range uses {
							u := ops[i].Value
							tiedAlready := slices.ContainsFunc(ops[uses:], func(o Operand) bool { return o.Tied && o.TiedUse == i })
							if !tiedAlready && g.values[u].Class == g.values[v].Class && g.words(u) == g.words(v) &&
								g.mask(ops[i].Fixed)&fixedDefs == 0 {
								candidates = append(candidates, i)
							}
						}
						if len(candidates) != 0 {
							op.Tied, op.TiedUse = true, candidates[rng.IntN(len(candidates))]
						}
					case 1:
						op.EarlyClobber = true
					case 2:
						if single(v) {
							r := general[rng.IntN(len(general))]
							tiedFixed := slices.ContainsFunc(ops[uses:], func(o Operand) bool {
								return o.Tied && ops[o.TiedUse].Fixed == g.single(r)
							})
							if fixedDefs&(1<<uint(r)) == 0 && !tiedFixed {
								op.Fixed = g.single(r)
								fixedDefs |= 1 << uint(r)
							}
						}
					}
					ops = append(ops, op)
				}
				emit(ops)
			}
		}
		// Terminator: a return in the last block, else a branch whose
		// edges pass arguments.
		if b == blocks-1 {
			v := pick(single)
			if v < 0 {
				v = constant()
			}
			g.returns[position] = true
			emit([]Operand{{Value: v}})
		} else {
			var ops []Operand
			if v := pick(func(int) bool { return true }); v >= 0 && rng.IntN(2) == 0 {
				ops = append(ops, Operand{Value: v})
			}
			emit(ops)
			for _, e := range g.succs[b] {
				for _, x := range params[g.edges[e].To] {
					arg := pick(func(v int) bool {
						return g.values[v].Class == g.values[x].Class && g.words(v) == g.words(x)
					})
					if arg < 0 {
						// Define a matching value before the terminator.
						arg = newValue(g.values[x].Class, g.values[x].Words, false)
						term := g.ops[len(g.ops)-1]
						g.ops[len(g.ops)-1] = []Operand{{Value: arg, Def: true}}
						available = append(available, arg)
						defined[b] = append(defined[b], arg)
						g.ops = append(g.ops, term)
						position++
					}
					g.edges[e].Args = append(g.edges[e].Args, arg)
				}
			}
		}
		g.blocks = append(g.blocks, Block{Start: start, End: position, Params: params[b]})
	}
	// Half the functions reserve registers in a tree of scopes, from their
	// own generator so the functions themselves stay the same.
	if srng := rand.New(rand.NewPCG(seed, 0x5c09e)); srng.IntN(2) == 0 {
		candidates := append(slices.Clone(general), vectorRegs...)
		var add func(lo, hi, parent, depth int)
		add = func(lo, hi, parent, depth int) {
			for cursor := lo; cursor < hi && depth < 4; {
				if srng.IntN(3) == 0 {
					break
				}
				start := cursor + srng.IntN(hi-cursor)
				end := start + 1 + srng.IntN(hi-start)
				var reserved []backend.Register
				for range srng.IntN(3) {
					reserved = append(reserved, c.Registers.At(candidates[srng.IntN(len(candidates))]))
				}
				g.scopes = append(g.scopes, Scope{Parent: parent, Start: start, End: end, Registers: backend.NewRegisterSet(reserved...)})
				add(start, end, len(g.scopes)-1, depth+1)
				cursor = end
			}
		}
		add(0, len(g.ops), -1, 0)
	}
	// Some uses read from memory, and some positions overwrite registers
	// besides their definitions, as an expansion's scratch registers.
	if mrng := rand.New(rand.NewPCG(seed, 0x3e3a0)); mrng.IntN(2) == 0 {
		candidates := append(slices.Clone(general), vectorRegs...)
		for p, ops := range g.ops {
			if g.calls[p] != nil || g.returns[p] {
				continue
			}
			for i := range ops {
				tiedTo := slices.ContainsFunc(ops, func(o Operand) bool { return o.Tied && o.TiedUse == i })
				if !ops[i].Def && ops[i].Fixed.Words == 0 && !tiedTo && mrng.IntN(5) == 0 {
					ops[i].Memory = true
				}
			}
			if mrng.IntN(6) == 0 {
				var list []backend.Register
				for range 1 + mrng.IntN(2) {
					list = append(list, c.Registers.At(candidates[mrng.IntN(len(candidates))]))
				}
				if g.clobbers == nil {
					g.clobbers = map[int]backend.RegisterSet{}
				}
				g.clobbers[p] = backend.NewRegisterSet(list...)
			}
		}
	}
	// A third of them prefer not to use their last general registers, as a
	// C entry's callee-saved ones.
	if prng := rand.New(rand.NewPCG(seed, 0x9e5e7)); prng.IntN(3) == 0 && len(general) > 1 {
		k := 1 + prng.IntN(len(general)-1)
		var preserved []backend.Register
		for _, index := range general[len(general)-k:] {
			preserved = append(preserved, c.Registers.At(index))
		}
		g.config.Preserved = backend.NewRegisterSet(preserved...)
	}
	return g
}

// dominatorSets computes each block's dominators iteratively.
func dominatorSets(blocks int, preds [][]int) [][]bool {
	dom := make([][]bool, blocks)
	for b := range blocks {
		dom[b] = make([]bool, blocks)
		for d := range blocks {
			dom[b][d] = b != 0 || d == 0
		}
	}
	for changed := true; changed; {
		changed = false
		for b := 1; b < blocks; b++ {
			next := make([]bool, blocks)
			for d := range blocks {
				next[d] = len(preds[b]) != 0
				for _, p := range preds[b] {
					next[d] = next[d] && dom[p][d]
				}
			}
			next[b] = true
			if !slices.Equal(next, dom[b]) {
				dom[b], changed = next, true
			}
		}
	}
	return dom
}

// machineState runs an Assignment: registers, frame slots and incoming
// stack words, with a reference value for every live value.
type machineState struct {
	t      *testing.T
	g      *genFunction
	w      *walkRecord // the walk being simulated
	regs   [256]uint64 // by register
	slots  map[int][2]uint64
	stack  map[int]uint64
	ref    map[int][2]uint64
	rng    *rand.Rand
	failed bool
	quiet  bool // a failure is expected: record it without reporting
	trace  func(format string, args ...any)
}

func (m *machineState) log(format string, args ...any) {
	if m.trace != nil {
		m.trace(format, args...)
	}
}

func (m *machineState) fail(format string, args ...any) {
	if !m.failed && !m.quiet {
		m.t.Errorf(format, args...)
	}
	m.failed = true
}

// readMemory reads v from where a Memory use reports it.
func (m *machineState) readMemory(v int, loc ValueLocation) [2]uint64 {
	var out [2]uint64
	switch {
	case loc.Slot >= 0:
		stored, ok := m.slots[loc.Slot]
		if !ok {
			m.fail("value %d read from unwritten slot %d", v, loc.Slot)
		}
		out = stored
	case loc.IncomingWords != 0:
		for k := range int(loc.IncomingWords) {
			out[k] = m.stack[loc.Incoming[k]]
		}
	default:
		constant, ok := m.g.constants[v]
		if !ok {
			m.fail("value %d read from memory has no location", v)
		}
		out = [2]uint64{constant}
	}
	if out != m.ref[v] {
		m.fail("value %d read from memory %+v is %x, want %x", v, loc, out, m.ref[v])
	}
	return out
}

func (m *machineState) read(v int, g Group) [2]uint64 {
	var out [2]uint64
	for i := 0; i < int(g.Words); i++ {
		out[i] = m.regs[g.Registers[i]]
	}
	if words := m.g.words(v); int(g.Words) != words {
		m.fail("value %d read from %v, want %d words", v, g, words)
	}
	if out != m.ref[v] {
		m.fail("value %d in %v is %x, want %x", v, g, out, m.ref[v])
	}
	return out
}

func (m *machineState) write(g Group, value [2]uint64) {
	for i := 0; i < int(g.Words); i++ {
		m.regs[g.Registers[i]] = value[i]
	}
}

func (m *machineState) checkSlot(slot, words int) {
	if slot < m.g.config.ReservedSlots || slot+words > m.w.slots {
		m.fail("slot %d (+%d) outside [%d, %d)", slot, words, m.g.config.ReservedSlots, m.w.slots)
	}
}

func (m *machineState) Spill(v int, slot int, g Group) {
	words := 1
	if m.g.values[v].Class == backend.Vector {
		words = 2
		if slot%2 != m.g.config.VectorSlotParity {
			m.fail("vector slot %d has the wrong parity", slot)
		}
	} else {
		words = m.g.words(v)
	}
	m.checkSlot(slot, words)
	m.log("spill v%d %v -> slot %d", v, g, slot)
	m.slots[slot] = m.read(v, g)
}

func (m *machineState) Reload(v int, g Group, slot int) {
	stored, ok := m.slots[slot]
	if !ok {
		m.fail("value %d loaded from unwritten slot %d", v, slot)
	}
	m.log("reload v%d %v <- slot %d", v, g, slot)
	m.load(v, g, stored)
}

func (m *machineState) LoadIncoming(v int, g Group, offsets [2]int) {
	var value [2]uint64
	for i := 0; i < int(g.Words); i++ {
		value[i] = m.stack[offsets[i]]
	}
	m.log("load v%d %v <- incoming %v", v, g, offsets)
	m.load(v, g, value)
}

func (m *machineState) Rematerialize(v int, g Group) error {
	constant, ok := m.g.constants[v]
	if !ok {
		m.fail("value %d rematerialized but is not a constant", v)
	}
	m.log("rematerialize v%d %v", v, g)
	m.load(v, g, [2]uint64{constant})
	return nil
}

func (m *machineState) load(v int, g Group, value [2]uint64) {
	if value != m.ref[v] {
		m.fail("value %d loaded as %x, want %x", v, value, m.ref[v])
	}
	m.write(g, value)
}

func (m *machineState) Move(v int, to, from Group) {
	m.log("move v%d %v <- %v", v, to, from)
	m.write(to, m.read(v, from))
}

func mix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	return x ^ x>>31
}

// run executes the function on random arguments and returns its result.
func (m *machineState) run(seed uint64) {
	g := m.g
	m.rng = rand.New(rand.NewPCG(seed, 7))
	for r := range g.config.Registers.All() {
		m.regs[r] = m.rng.Uint64()
	}
	m.slots, m.stack, m.ref = map[int][2]uint64{}, map[int]uint64{}, map[int][2]uint64{}
	for v, c := range g.constants {
		m.ref[v] = [2]uint64{c}
	}
	for i, x := range g.blocks[0].Params {
		value := [2]uint64{m.rng.Uint64()}
		if g.words(x) == 2 {
			value[1] = m.rng.Uint64()
		}
		m.ref[x] = value
		for w, word := range g.layout.Arguments[i].Words {
			if word.Storage == abi.Frame {
				if w == 0 {
					m.slots[word.Slot] = value // a slot holds a whole value
				}
			} else if word.Storage == abi.Stack {
				m.stack[word.StackOffset] = value[w]
			} else {
				m.regs[registerNamed(g.config.Registers, word.Register)] = value[w]
			}
		}
	}
	if err := m.w.replay(m.w.entry, m); err != nil {
		m.fail("entry: %v", err)
	}
	b, steps := 0, 0
	for !m.failed {
		block := g.blocks[b]
		for p := block.Start; p < block.End && !m.failed; p++ {
			m.log("position %d", p)
			if err := m.w.replay(m.w.before[p], m); err != nil {
				m.fail("before %d: %v", p, err)
			}
			effect := g.calls[p]
			hash := uint64(p)
			arg := 0
			var useLocs []Group
			for i, op := range g.ops[p] {
				if op.Def {
					useLocs = append(useLocs, Group{})
					continue
				}
				if op.Memory {
					useLocs = append(useLocs, Group{})
					value := m.readMemory(op.Value, m.w.memory[p][i])
					hash = mix(hash ^ value[0] ^ value[1]<<1)
					if effect != nil {
						arg++
					}
					continue
				}
				loc := m.w.operands[p][i]
				switch {
				case op.Fixed.Words != 0:
					loc = op.Fixed
				case effect != nil && arg < len(effect.ArgumentLocations) && effect.ArgumentLocations[arg].Words[0].Storage == abi.Register:
					loc = SingleRegister(registerNamed(g.config.Registers, effect.ArgumentLocations[arg].Words[0].Register))
				case g.returns[p]:
					loc = SingleRegister(registerNamed(g.config.Registers, g.layout.Result.Words[0].Register))
				}
				if effect != nil {
					arg++
				}
				useLocs = append(useLocs, loc)
				value := m.read(op.Value, loc)
				hash = mix(hash ^ value[0] ^ value[1]<<1)
			}
			if g.returns[p] {
				return
			}
			// A two-address instruction reads its tied source from the
			// definition's registers.
			for i, op := range g.ops[p] {
				if op.Def && op.Tied {
					m.read(g.ops[p][op.TiedUse].Value, m.w.operands[p][i])
				}
			}
			// The client may use the registers reserved here, and the
			// instruction overwrites its clobbers.
			for r := range g.reservedAt(p).All() {
				m.regs[r] = m.rng.Uint64()
			}
			for r := range g.clobbers[p].All() {
				m.regs[r] = m.rng.Uint64()
			}
			if effect != nil {
				for _, name := range effect.ClobberedRegisters {
					m.regs[registerNamed(g.config.Registers, name)] = m.rng.Uint64()
				}
			}
			for i, op := range g.ops[p] {
				if !op.Def {
					continue
				}
				if g.values[op.Value].Constant {
					continue
				}
				value := [2]uint64{mix(hash + uint64(i)), mix(hash + uint64(i) + 100)}
				if g.words(op.Value) == 1 {
					value[1] = 0
				}
				m.ref[op.Value] = value
				loc := m.w.operands[p][i]
				want := op.Fixed
				if effect != nil && effect.ResultLocation.Words != nil {
					want = SingleRegister(registerNamed(g.config.Registers, effect.ResultLocation.Words[0].Register))
				}
				if want.Words != 0 && loc != want {
					m.fail("position %d defines %d in %v, want %v", p, op.Value, loc, want)
				}
				if op.EarlyClobber && g.mask(loc)&func() uint64 {
					var mask uint64
					for _, l := range useLocs {
						mask |= g.mask(l)
					}
					return mask
				}() != 0 {
					m.fail("position %d early-clobber definition %d shares a use register", p, op.Value)
				}
				m.write(loc, value)
			}
			if err := m.w.replay(m.w.after[p], m); err != nil {
				m.fail("after %d: %v", p, err)
			}
		}
		if m.failed {
			return
		}
		succs := g.succs[b]
		e := succs[m.rng.IntN(len(succs))]
		if steps++; steps > 40 {
			for _, s := range succs {
				if g.edges[s].To > g.edges[e].To {
					e = s
				}
			}
		}
		edge := g.edges[e]
		m.log("edge %d %+v", e, edge)
		if err := m.w.replay(m.w.edges[e], m); err != nil {
			m.fail("edge %d: %v", e, err)
		}
		var passed [][2]uint64
		for _, a := range edge.Args {
			passed = append(passed, m.ref[a])
		}
		for i, x := range g.blocks[edge.To].Params {
			m.ref[x] = passed[i]
		}
		b = edge.To
	}
}

// testAllocators returns a fresh instance of every Allocator.
func testAllocators() []struct {
	name string
	a    Allocator
} {
	return []struct {
		name string
		a    Allocator
	}{{"batch", &Batch{}}, {"online", &Online{}}}
}

// TestAllocatorsRandomFunctions runs random functions through every
// Allocator, then simulates each walk on random paths and verifies it.
func TestAllocatorsRandomFunctions(t *testing.T) {
	for _, c := range testAllocators() {
		t.Run(c.name, func(t *testing.T) {
			limits := 0
			const functions = 4000
			for seed := range uint64(functions) {
				g := generate(seed)
				w, err := recordWalk(g.config, g.function(), c.a)
				if errors.Is(err, backend.ErrBackendLimit) {
					limits++
					continue
				}
				if err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
				for run := range uint64(4) {
					m := &machineState{t: t, g: g, w: w}
					m.run(seed*16 + run)
					if m.failed {
						t.Fatalf("seed %d run %d failed", seed, run)
					}
				}
				if err := Verify(g.config, g.function(), c.a); err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
			}
			if limits > functions/20 {
				t.Errorf("%d of %d functions hit backend limits", limits, functions)
			}
			t.Logf("%d of %d functions hit backend limits", limits, functions)
		})
	}
}

// A walk the client abandons leaves no state behind: the next function
// starts clean even when the abandoned position read a use from memory.
func TestAllocatorsReuseAfterAbandonedWalk(t *testing.T) {
	for _, c := range testAllocators() {
		t.Run(c.name, func(t *testing.T) { reuseAfterAbandonedWalk(t, c.a) })
	}
}

func reuseAfterAbandonedWalk(t *testing.T, a Allocator) {
	var abandoned FunctionBuilder
	for range 6 {
		abandoned.AddValue(Value{})
	}
	abandoned.SetLayout(abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("r0")}, Result: registerLoc("r0")})
	abandoned.AddBlock(5)
	abandoned.AddPosition(Operand{Value: 5, Memory: true}, Operand{Value: 0, Def: true})
	abandoned.AddReturn(Operand{Value: 0})
	config := Config{Registers: named(gprs(3), names(3)), SlotLimit: 16}
	if err := a.Allocate(config, &abandoned, &walkRecorder{into: new([]walkMove)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Entry(); err != nil {
		t.Fatal(err)
	}
	if err := a.Before(0); err != nil {
		t.Fatal(err)
	}
	// The client stops here, then allocates a smaller function.
	g, f := straightFunction(4)
	if err := Verify(g.config, f, a); err != nil {
		t.Fatal(err)
	}
}
