package regalloc

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

func names(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "r" + string(rune('0'+i))
	}
	return out
}

func registerLoc(name string) abi.ValueLocation {
	return abi.ValueLocation{Words: []abi.WordLocation{{Storage: abi.Register, Register: name}}}
}

// swapLoop is entry(x, y) -> loop(a, b) -> loop(b, a) or exit(a): the back
// edge swaps two block parameters. Every value is used, so a and b stay live
// in registers across the back edge.
func swapLoop() *genFunction {
	g := &genFunction{constants: map[int]uint64{}, calls: map[int]*abi.CallEffect{}, returns: map[int]bool{}}
	g.config = Config{Registers: named(gprs(3), names(3)), SlotLimit: 16}
	g.values = make([]Value, 4) // x0 x1 entry, a2 b3 loop parameters
	g.layout = abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("r0"), registerLoc("r1")}, Result: registerLoc("r0")}
	g.ops = [][]Operand{
		{},                       // entry: jump
		{{Value: 2}, {Value: 3}}, // loop: branch reading both
		{{Value: 2}},             // exit: return a
	}
	g.returns[2] = true
	g.blocks = []Block{{Start: 0, End: 1, Params: []int{0, 1}}, {Start: 1, End: 2, Params: []int{2, 3}}, {Start: 2, End: 3}}
	g.edges = []Edge{{From: 0, To: 1, Args: []int{0, 1}}, {From: 1, To: 1, Args: []int{3, 2}}, {From: 1, To: 2}}
	g.succs = [][]int{{0}, {1, 2}, nil}
	return g
}

// runGenerated simulates a recorded walk of g on runs random paths.
func runGenerated(t *testing.T, g *genFunction, w *walkRecord, runs int) {
	t.Helper()
	for run := range uint64(runs) {
		m := &machineState{t: t, g: g, w: w}
		m.run(run)
		if m.failed {
			t.Fatalf("run %d failed", run)
		}
	}
}

func TestBatchSwapsThroughScratchOrSlot(t *testing.T) {
	for _, scratch := range []bool{false, true} {
		g := swapLoop()
		if scratch {
			g.config.Registers = named(gprs(4), names(4))
			g.config.Blocked = testSet(g.config.Registers, 1<<3)
			g.config.Scratch = g.config.Blocked
		}
		var a Batch
		s, err := recordWalk(g.config, g.function(), &a)
		if err != nil {
			t.Fatal(err)
		}
		if s.edgeEmpty[1] {
			t.Fatalf("scratch=%v: swapping back edge has no moves", scratch)
		}
		if want := 0; scratch && s.slots != want {
			t.Errorf("scratch=%v: %d slots, want %d (the cycle should use the scratch register)", scratch, s.slots, want)
		}
		if !scratch && s.slots == 0 {
			t.Errorf("scratch=%v: no temporary slot for the cycle", scratch)
		}
		runGenerated(t, g, s, 8)
		a.Finish()
	}
}

// With one register, the swapped parameters live in a register and a frame
// slot: the back edge must read the old slot before storing the new value.
func TestBatchSwapsParameterSlots(t *testing.T) {
	g := swapLoop()
	g.config.Registers = named(gprs(1), names(1))
	g.layout = abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("r0"), {Words: []abi.WordLocation{{Storage: abi.Stack, StackOffset: 8}}}},
		Result: registerLoc("r0")}
	// b is read first, so a is in the register at the back edge and b in
	// its slot: a <- b loads the slot that b <- a stores.
	g.ops = [][]Operand{{}, {{Value: 3}}, {{Value: 2}}, {{Value: 2}}}
	g.returns = map[int]bool{3: true}
	g.blocks = []Block{{Start: 0, End: 1, Params: []int{0, 1}}, {Start: 1, End: 3, Params: []int{2, 3}}, {Start: 3, End: 4}}
	var a Batch
	s, err := recordWalk(g.config, g.function(), &a)
	if err != nil {
		t.Fatal(err)
	}
	runGenerated(t, g, s, 8)
}

func TestBatchEdgesWithoutMovesAreEmpty(t *testing.T) {
	// entry(x) branches to two blocks that both jump to join; x stays put.
	g := &genFunction{constants: map[int]uint64{}, calls: map[int]*abi.CallEffect{}, returns: map[int]bool{}}
	g.config = Config{Registers: named(gprs(4), names(4)), SlotLimit: 16}
	g.values = make([]Value, 1)
	g.layout = abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("r0")}, Result: registerLoc("r0")}
	g.ops = [][]Operand{{{Value: 0}}, {}, {}, {{Value: 0}}}
	g.returns[3] = true
	g.blocks = []Block{{Start: 0, End: 1, Params: []int{0}}, {Start: 1, End: 2}, {Start: 2, End: 3}, {Start: 3, End: 4}}
	g.edges = []Edge{{From: 0, To: 1}, {From: 0, To: 2}, {From: 1, To: 3}, {From: 2, To: 3}}
	g.succs = [][]int{{0, 1}, {2}, {3}, nil}
	var a Batch
	s, err := recordWalk(g.config, g.function(), &a)
	if err != nil {
		t.Fatal(err)
	}
	for e := range g.edges {
		if !s.edgeEmpty[e] {
			t.Errorf("edge %d has moves", e)
		}
	}
	if s.slots != 0 {
		t.Errorf("%d slots", s.slots)
	}
	runGenerated(t, g, s, 4)
}

// loadCounter is a Mover that counts loads.
type loadCounter struct{ loads []int }

func (m *loadCounter) Spill(int, int, Group)                 {}
func (m *loadCounter) Move(int, Group, Group)                {}
func (m *loadCounter) Reload(v int, _ Group, _ int)          { m.loads = append(m.loads, v) }
func (m *loadCounter) LoadIncoming(v int, _ Group, _ [2]int) { m.loads = append(m.loads, v) }
func (m *loadCounter) Rematerialize(v int, _ Group) error {
	m.loads = append(m.loads, v)
	return nil
}

// passThroughLoop is entry(a, b) -> loop: read a, define and read t, then
// loop or exit to return b. Only a and t are read in the loop.
func passThroughLoop(registers int) *genFunction {
	g := &genFunction{constants: map[int]uint64{}, calls: map[int]*abi.CallEffect{}, returns: map[int]bool{}}
	g.config = Config{Registers: named(gprs(registers), names(registers)), SlotLimit: 16}
	g.values = make([]Value, 3) // a0 b1 entry, t2
	g.layout = abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("r0"), registerLoc("r1")}, Result: registerLoc("r0")}
	g.ops = [][]Operand{{}, {{Value: 0}}, {{Value: 2, Def: true}}, {{Value: 2}}, {{Value: 1}}}
	g.returns[4] = true
	g.blocks = []Block{{Start: 0, End: 1, Params: []int{0, 1}}, {Start: 1, End: 4}, {Start: 4, End: 5}}
	g.edges = []Edge{{From: 0, To: 1}, {From: 1, To: 1}, {From: 1, To: 2}}
	g.succs = [][]int{{0}, {1, 2}, nil}
	return g
}

// With two registers, b must leave its register for t. It does so on entry
// to the loop, which never reads it, so the loop has no loads; with three,
// nothing is spilled.
func TestBatchKeepsLoopValuesInRegisters(t *testing.T) {
	for _, registers := range []int{2, 3} {
		g := passThroughLoop(registers)
		var a Batch
		s, err := recordWalk(g.config, g.function(), &a)
		if err != nil {
			t.Fatal(err)
		}
		var loop loadCounter
		for p := 1; p < 4; p++ {
			if err := errors.Join(s.replay(s.before[p], &loop), s.replay(s.after[p], &loop), s.replay(s.edges[1], &loop)); err != nil {
				t.Fatal(err)
			}
		}
		if len(loop.loads) != 0 {
			t.Errorf("%d registers: loop loads values %v", registers, loop.loads)
		}
		var exit loadCounter
		if err := errors.Join(s.replay(s.edges[2], &exit), s.replay(s.before[4], &exit)); err != nil {
			t.Fatal(err)
		}
		if want := registers == 2; (len(exit.loads) != 0) != want || (s.slots != 0) != want {
			t.Errorf("%d registers: exit loads %v, %d slots", registers, exit.loads, s.slots)
		}
		runGenerated(t, g, s, 4)
		a.Finish()
	}
}

func TestBatchReport(t *testing.T) {
	g := swapLoop()
	g.config.Registers = named(testRegisters(5, 3<<3), []string{"a", "b", "c", "v0", "v1"})
	g.layout = abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("a"), registerLoc("b")}, Result: registerLoc("a")}
	// A call clobbering c and v1 between the loop and the exit.
	g.ops[2] = []Operand{{Value: 2}}
	g.ops = append(g.ops[:2], []Operand{}, g.ops[2])
	g.calls = map[int]*abi.CallEffect{2: {ClobberedRegisters: []string{"c", "unknown"}, ClobberedFPRegisters: []string{"v1"}}}
	g.returns = map[int]bool{3: true}
	g.blocks[2].End = 4
	var a Batch
	if err := a.Allocate(g.config, g.function(), nil); err != nil {
		t.Fatal(err)
	}
	var r abi.EmissionReport
	a.Report(&r)
	if !slices.Contains(r.ClobberedRegisters, "c") || slices.Contains(r.ClobberedRegisters, "unknown") ||
		!slices.Equal(r.ClobberedFPRegisters, []string{"v1"}) {
		t.Errorf("report %v / %v", r.ClobberedRegisters, r.ClobberedFPRegisters)
	}
	g.config.Registers = backend.NewRegisterSet(g.config.Registers.AppendTo(nil)...)
	g.layout = abi.EntryLayout{}
	g.blocks[0].Params = nil
	g.values = append(g.values, Value{Constant: true}, Value{Constant: true})
	g.constants[4], g.constants[5] = 1, 2
	g.ops[0] = []Operand{{Value: 4, Def: true}, {Value: 5, Def: true}}
	g.edges[0].Args = []int{4, 5}
	g.calls = nil
	g.returns = nil
	if err := a.Allocate(g.config, g.function(), nil); err != nil {
		t.Fatal(err)
	}
	a.Report(&r)
	if r.ClobberedRegisters != nil || r.ClobberedFPRegisters != nil {
		t.Errorf("report without names %v / %v", r.ClobberedRegisters, r.ClobberedFPRegisters)
	}
	if g := a.Operand(0, 0); g.Words != 0 {
		t.Errorf("constant definition has registers %v", g)
	}
}

func TestAllocatorsRejectInvalidFunctions(t *testing.T) {
	base := func() *genFunction { return swapLoop() }
	cases := []struct {
		name   string
		change func(g *genFunction)
		want   error
	}{
		{"defined twice", func(g *genFunction) { g.ops[0] = []Operand{{Value: 2, Def: true}} }, backend.ErrInvalidFunction},
		{"used undefined", func(g *genFunction) {
			g.values = append(g.values, Value{})
			g.ops[1] = append(g.ops[1], Operand{Value: 4})
		}, backend.ErrInvalidFunction},
		{"not dominated", func(g *genFunction) {
			g.values = append(g.values, Value{})
			g.ops[1] = append(g.ops[1], Operand{Value: 4, Def: true})
			g.ops[0] = []Operand{{Value: 4}}
		}, backend.ErrInvalidFunction},
		{"value out of range", func(g *genFunction) { g.ops[1][0].Value = 9 }, backend.ErrInvalidFunction},
		{"argument count", func(g *genFunction) { g.edges[1].Args = g.edges[1].Args[:1] }, backend.ErrInvalidFunction},
		{"edge into entry", func(g *genFunction) { g.edges[2].To = 0 }, backend.ErrInvalidFunction},
		{"blocks leave a gap", func(g *genFunction) { g.blocks[1].End = 1; g.blocks[1].Start = 1 }, backend.ErrInvalidFunction},
		{"two values fixed to one register", func(g *genFunction) {
			g.ops[1][0].Fixed, g.ops[1][1].Fixed = SingleRegister(1), SingleRegister(1)
		}, backend.ErrInvalidFunction},
		{"unknown abi register", func(g *genFunction) { g.layout.Arguments[0] = registerLoc("x9") }, backend.ErrInvalidFunction},
		{"entry arguments", func(g *genFunction) { g.layout.Arguments = g.layout.Arguments[:1] }, backend.ErrInvalidFunction},
		{"call without its arguments", func(g *genFunction) {
			g.calls[1] = &abi.CallEffect{ArgumentLocations: []abi.ValueLocation{registerLoc("r0"), registerLoc("r1"), registerLoc("r2")}}
		}, backend.ErrInvalidFunction},
		{"scope outside its parent", func(g *genFunction) {
			g.scopes = []Scope{{Parent: -1, Start: 1, End: 3}, {Parent: 0, Start: 0, End: 2}}
		}, backend.ErrInvalidFunction},
		{"overlapping scopes", func(g *genFunction) {
			g.scopes = []Scope{{Parent: -1, Start: 0, End: 2}, {Parent: -1, Start: 1, End: 3}}
		}, backend.ErrInvalidFunction},
		{"scope parent cycle", func(g *genFunction) {
			g.scopes = []Scope{{Parent: 1, Start: 0, End: 2}, {Parent: 0, Start: 0, End: 2}}
		}, backend.ErrInvalidFunction},
		{"empty scope", func(g *genFunction) { g.scopes = []Scope{{Parent: -1, Start: 1, End: 1}} }, backend.ErrInvalidFunction},
		{"scope beyond the positions", func(g *genFunction) { g.scopes = []Scope{{Parent: -1, Start: 0, End: 99}} }, backend.ErrInvalidFunction},
		{"scope reserves an unknown register", func(g *genFunction) {
			g.scopes = []Scope{{Parent: -1, Start: 0, End: 1, Registers: backend.NewRegisterSet(40)}}
		}, backend.ErrInvalidFunction},
		{"definition read from memory", func(g *genFunction) { g.ops[0] = []Operand{{Value: 2, Def: true, Memory: true}} }, backend.ErrInvalidFunction},
		{"fixed use read from memory", func(g *genFunction) {
			g.ops[1][0].Memory, g.ops[1][0].Fixed = true, SingleRegister(1)
		}, backend.ErrInvalidFunction},
		{"tie to a use read from memory", func(g *genFunction) {
			g.values = append(g.values, Value{})
			g.ops[1][0].Memory = true
			g.ops[1] = append(g.ops[1], Operand{Value: 4, Def: true, Tied: true, TiedUse: 0})
		}, backend.ErrInvalidFunction},
		{"returned value read from memory", func(g *genFunction) { g.ops[2][0].Memory = true }, backend.ErrInvalidFunction},
		{"tie to a definition", func(g *genFunction) {
			g.values = append(g.values, Value{}, Value{})
			g.ops[1] = append(g.ops[1], Operand{Value: 4, Def: true}, Operand{Value: 5, Def: true, Tied: true, TiedUse: 2})
		}, backend.ErrInvalidFunction},
	}
	for _, a := range testAllocators() {
		for _, c := range cases {
			if _, online := a.a.(*Online); online && c.name == "not dominated" {
				continue // Online does not check dominance
			}
			g := base()
			c.change(g)
			if err := a.a.Allocate(g.config, g.function(), nil); !errors.Is(err, c.want) {
				t.Errorf("%s: %s: %v, want %v", a.name, c.name, err, c.want)
			}
		}
		// The slot limit reports through LimitError.
		g := base()
		g.config.Registers, g.config.SlotLimit = named(gprs(2), names(2)), 0
		limit := errors.New("limit")
		g.config.LimitError = func(int) error { return limit }
		if err := a.a.Allocate(g.config, g.function(), nil); !errors.Is(err, limit) {
			t.Errorf("%s: slot limit: %v", a.name, err)
		}
	}
}

func TestBatchBuffers(t *testing.T) {
	var a Batch
	g := generate(7)
	f := g.function()
	if err := a.Allocate(g.config, f, nil); err != nil {
		t.Fatal(err)
	}
	a.Finish()
	retained, big := a.RetainedBytes(1 << 30)
	if retained == 0 || big {
		t.Fatalf("retained %d big=%v", retained, big)
	}
	allocs := testing.AllocsPerRun(20, func() {
		if err := a.Allocate(g.config, f, nil); err != nil {
			t.Fatal(err)
		}
		a.Finish()
	})
	if allocs > 2 {
		t.Errorf("reallocating the same function allocates %v times", allocs)
	}
	if freed, _ := a.TrimToBudget(0); freed == 0 {
		t.Error("TrimToBudget freed nothing")
	}
	if retained, _ := a.RetainedBytes(0); retained != 0 {
		t.Errorf("%d bytes retained after trimming to zero", retained)
	}
	if err := a.Allocate(g.config, f, nil); err != nil {
		t.Fatal(err)
	}
	a.Finish()
	a.Trim(0)
	if retained, _ := a.RetainedBytes(0); retained != 0 {
		t.Errorf("%d bytes retained after Trim(0)", retained)
	}
	for range 64 {
		a.Decay()
	}
}

// A reused Batch sizes each function's frame afresh: edge temporaries one
// function needed are not reserved for the next.
func TestBatchReuseResetsEdgeTemporaries(t *testing.T) {
	var reused Batch
	for seed := range uint64(300) {
		g := generate(seed)
		var fresh Batch
		if err := fresh.Allocate(g.config, g.function(), nil); err != nil {
			continue
		}
		if err := reused.Allocate(g.config, g.function(), nil); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if reused.Slots() != fresh.Slots() {
			t.Fatalf("seed %d: reused allocator uses %d slots, fresh %d", seed, reused.Slots(), fresh.Slots())
		}
		reused.Finish()
		fresh.Finish()
	}
}

// Batch, like Residency, takes a preserved register only once a free other
// register or a value with a memory copy is not to be had: over many
// functions it puts values in fewer of them than it would without the rule.
func TestBatchDefersPreservedRegisters(t *testing.T) {
	taken := func(g *genFunction, config Config) (int, bool) {
		w, err := recordWalk(config, g.function(), &Batch{})
		if err != nil {
			return 0, false
		}
		var used Pins
		note := func(moves []walkMove) {
			for _, m := range moves {
				if m.kind != recSpill {
					used.Add(m.to)
				}
			}
		}
		note(w.entry)
		for p := range w.before {
			note(w.before[p])
			note(w.after[p])
			for _, group := range w.operands[p] {
				used.Add(group)
			}
		}
		for _, moves := range w.edges {
			note(moves)
		}
		n := 0
		for r := range g.config.Preserved.All() {
			if used.Contains(r) {
				n++
			}
		}
		return n, true
	}
	with, without, functions := 0, 0, 0
	for seed := range uint64(3000) {
		g := generate(seed)
		if g.config.Preserved.Len() == 0 {
			continue
		}
		plain := g.config
		plain.Preserved = backend.RegisterSet{}
		n, ok := taken(g, g.config)
		m, okPlain := taken(g, plain)
		if !ok || !okPlain {
			continue
		}
		with, without, functions = with+n, without+m, functions+1
	}
	if functions == 0 || with >= without {
		t.Fatalf("preserved registers holding values: %d with the rule, %d without, over %d functions", with, without, functions)
	}
	t.Logf("preserved registers holding values: %d with the rule, %d without, over %d functions", with, without, functions)
}
