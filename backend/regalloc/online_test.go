package regalloc

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

// A counter carried around a two-block loop: the header's parameter passes
// unchanged to the body's, which passes its successor back.
func counterLoop() *genFunction {
	g := &genFunction{constants: map[int]uint64{}, calls: map[int]*abi.CallEffect{}, returns: map[int]bool{}}
	g.config = Config{Registers: named(gprs(3), names(3)), SlotLimit: 16}
	g.values = make([]Value, 4) // x0 entry, h1 header, b2 body, n3 next
	g.layout = abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("r0")}, Result: registerLoc("r0")}
	g.ops = [][]Operand{{}, {{Value: 1}}, {{Value: 2}, {Value: 3, Def: true}}, {{Value: 1}}}
	g.returns[3] = true
	g.blocks = []Block{{Start: 0, End: 1, Params: []int{0}}, {Start: 1, End: 2, Params: []int{1}}, {Start: 2, End: 3, Params: []int{2}}, {Start: 3, End: 4}}
	g.edges = []Edge{{From: 0, To: 1, Args: []int{0}}, {From: 1, To: 2, Args: []int{1}}, {From: 2, To: 1, Args: []int{3}}, {From: 1, To: 3}}
	g.succs = [][]int{{0}, {1, 3}, {2}, nil}
	return g
}

// Parameters flow through registers: in the counter loop, the header passes
// its parameter on unchanged and the body passes its successor back, so
// neither edge touches memory and the pass-through edge has no moves.
func TestOnlineKeepsParametersInRegisters(t *testing.T) {
	g := counterLoop()
	// The exit takes the header's parameter as an argument of its own.
	g.values = append(g.values, Value{})
	g.blocks[3].Params, g.edges[3].Args, g.ops[3] = []int{4}, []int{1}, []Operand{{Value: 4}}
	w, err := recordWalk(g.config, g.function(), &Online{})
	if err != nil {
		t.Fatal(err)
	}
	runGenerated(t, g, w, 6)
	if err := Verify(g.config, g.function(), &Online{}); err != nil {
		t.Fatal(err)
	}
	if !w.edgeEmpty[1] {
		t.Errorf("pass-through edge moves %+v", w.edges[1])
	}
	for e, moves := range w.edges {
		for _, m := range moves {
			if m.kind == recSpill || m.kind == recReload {
				t.Errorf("edge %d goes through memory: %+v", e, moves)
			}
		}
	}
	for p := range w.before {
		if len(w.before[p])+len(w.after[p]) != 0 && p > 0 {
			t.Errorf("position %d moves %+v / %+v", p, w.before[p], w.after[p])
		}
	}
}

func TestOnlineReport(t *testing.T) {
	g := swapLoop()
	g.config.Registers = named(testRegisters(5, 3<<3), []string{"a", "b", "c", "v0", "v1"})
	g.layout = abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("a"), registerLoc("b")}, Result: registerLoc("a")}
	g.ops[2] = []Operand{{Value: 2}}
	g.ops = append(g.ops[:2], []Operand{}, g.ops[2])
	g.calls = map[int]*abi.CallEffect{2: {ClobberedRegisters: []string{"c", "unknown"}, ClobberedFPRegisters: []string{"v1"}}}
	g.returns = map[int]bool{3: true}
	g.blocks[2].End = 4
	var a Online
	if err := a.Allocate(g.config, g.function(), &walkRecorder{into: new([]walkMove)}); err != nil {
		t.Fatal(err)
	}
	var r abi.EmissionReport
	a.Report(&r) // before the walk: nothing written yet
	if len(r.ClobberedRegisters) != 0 {
		t.Errorf("report before the walk: %v", r.ClobberedRegisters)
	}
	if err := a.Entry(); err != nil {
		t.Fatal(err)
	}
	for p := range g.ops {
		if err := a.Before(p); err != nil {
			t.Fatal(err)
		}
		if err := a.After(p); err != nil {
			t.Fatal(err)
		}
	}
	a.Report(&r)
	if !slices.Contains(r.ClobberedRegisters, "c") || slices.Contains(r.ClobberedRegisters, "unknown") ||
		!slices.Equal(r.ClobberedFPRegisters, []string{"v1"}) {
		t.Errorf("report %v / %v", r.ClobberedRegisters, r.ClobberedFPRegisters)
	}
	a.Finish()
}

// A definition takes its Prefer when the registers are free, even over a
// tie, and keeps the tie when a live value or a use holds them.
func TestOnlinePrefer(t *testing.T) {
	config := Config{Registers: named(gprs(4), names(4)), SlotLimit: 16}
	build := func(b *FunctionBuilder, prefer backend.Register, keep, early bool) {
		b.Reset()
		x, y, z := b.AddValue(Value{}), b.AddValue(Value{}), b.AddValue(Value{})
		sum := b.AddValue(Value{})
		b.AddBlock(x, y, z)
		b.AddPosition(Operand{Value: x}, Operand{Value: y}, Operand{Value: sum, Def: true, Tied: !early, EarlyClobber: early, Prefer: SingleRegister(prefer)})
		ops := []Operand{{Value: sum}}
		if keep {
			ops = append(ops, Operand{Value: z}) // z stays live in r2
		}
		b.AddReturn(ops...)
		b.SetLayout(abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("r0"), registerLoc("r1"), registerLoc("r2")}, Result: registerLoc("r3")})
	}
	for _, c := range []struct {
		prefer      backend.Register
		keep, early bool
		want        backend.Register
	}{
		{3, false, false, 3}, // free: taken over the tie
		{2, true, false, 0},  // holds live z: tied
		{1, false, false, 0}, // holds use y: tied
		{2, false, true, 2},  // free, for an early clobber
		{1, false, true, 2},  // holds use y: the free register
	} {
		var b FunctionBuilder
		build(&b, c.prefer, c.keep, c.early)
		var o Online
		if err := o.Allocate(config, &b, &walkRecorder{into: new([]walkMove)}); err != nil {
			t.Fatal(err)
		}
		if err := o.Entry(); err != nil {
			t.Fatal(err)
		}
		if err := o.Before(0); err != nil {
			t.Fatal(err)
		}
		if got := o.Operand(0, 2); got != SingleRegister(c.want) {
			t.Errorf("prefer %d keep %v: definition in %v, want r%d", c.prefer, c.keep, got, c.want)
		}
		o.Finish()
		for _, a := range testAllocators() {
			if err := Verify(config, &b, a.a); err != nil {
				t.Errorf("%s prefer %d keep %v: %v", a.name, c.prefer, c.keep, err)
			}
		}
	}
}

func TestAllocatorsRejectBadPrefer(t *testing.T) {
	config := Config{Registers: named(testRegisters(3, 1<<2), names(3)), SlotLimit: 16}
	for _, c := range []struct {
		use, def Group
		ok       bool
	}{
		{Group{}, SingleRegister(1), true},
		{SingleRegister(0), Group{}, false},                         // on a use
		{Group{}, SingleRegister(7), false},                         // not allocatable
		{Group{}, SingleRegister(backend.VectorRegister(2)), false}, // another class
		{Group{}, Group{Registers: [2]backend.Register{0, 1}, Words: 2}, false},
	} {
		var b FunctionBuilder
		x, y := b.AddValue(Value{}), b.AddValue(Value{})
		b.AddBlock(x)
		b.AddPosition(Operand{Value: x, Prefer: c.use}, Operand{Value: y, Def: true, Prefer: c.def})
		b.AddReturn(Operand{Value: y})
		b.SetLayout(abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("r0")}, Result: registerLoc("r0")})
		for _, a := range testAllocators() {
			err := a.a.Allocate(config, &b, &walkRecorder{into: new([]walkMove)})
			if c.ok && err != nil || !c.ok && !errors.Is(err, backend.ErrInvalidFunction) {
				t.Errorf("%s %+v: Allocate = %v", a.name, c, err)
			}
			a.a.Finish()
		}
	}
}

// opaque hides a FunctionBuilder, so Online reads it through Function.
type opaque struct{ Function }

// Online reads a FunctionBuilder in place and lays out its uses as it
// reads; the walk is the one any other Function gets.
func TestOnlineReadsBuilderAsAnyFunction(t *testing.T) {
	for seed := range uint64(2000) {
		g := generate(seed)
		direct, err := recordWalk(g.config, g.function(), &Online{})
		wrapped, wrappedErr := recordWalk(g.config, opaque{g.function()}, &Online{})
		if fmt.Sprint(err) != fmt.Sprint(wrappedErr) {
			t.Fatalf("seed %d: builder %v, function %v", seed, err, wrappedErr)
		}
		if a, b := fmt.Sprintf("%+v", direct), fmt.Sprintf("%+v", wrapped); a != b {
			t.Fatalf("seed %d: walks differ\nbuilder  %s\nfunction %s", seed, a, b)
		}
	}
}

// A block laid out before its dominator reads a value defined later in
// layout order: read cannot lay its uses out as it goes and leaves them to
// assignHomes. A value never defined is rejected either way.
func TestOnlineReadsUsesBeforeDefinitions(t *testing.T) {
	config := Config{Registers: named(gprs(3), names(3)), SlotLimit: 16}
	for _, defined := range []bool{true, false} {
		var b FunctionBuilder
		x, v := b.AddValue(Value{}), b.AddValue(Value{})
		entry := b.AddBlock(x)
		b.AddPosition(Operand{Value: x})
		exit := b.AddBlock()
		b.AddReturn(Operand{Value: v})
		middle := b.AddBlock()
		if defined {
			b.AddPosition(Operand{Value: v, Def: true})
		} else {
			b.AddPosition()
		}
		b.AddEdge(entry, middle)
		b.AddEdge(middle, exit)
		b.SetLayout(abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("r0")}, Result: registerLoc("r0")})
		direct, err := recordWalk(config, &b, &Online{})
		wrapped, wrappedErr := recordWalk(config, opaque{&b}, &Online{})
		if (err == nil) != defined || fmt.Sprint(err) != fmt.Sprint(wrappedErr) {
			t.Fatalf("defined %v: builder %v, function %v", defined, err, wrappedErr)
		}
		if a, c := fmt.Sprintf("%+v", direct), fmt.Sprintf("%+v", wrapped); a != c {
			t.Fatalf("walks differ\nbuilder  %s\nfunction %s", a, c)
		}
		if defined {
			if err := Verify(config, &b, &Online{}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A value that lives past a call moves to a free register the call keeps
// rather than to memory, and an argument that lives on is copied to its
// register: x is the call's argument and is read again after it.
func TestOnlineKeepsValuesAcrossCalls(t *testing.T) {
	g := &genFunction{constants: map[int]uint64{}, calls: map[int]*abi.CallEffect{}, returns: map[int]bool{}}
	g.config = Config{Registers: named(gprs(4), names(4)), SlotLimit: 16}
	g.values = make([]Value, 3) // x0 entry, y1 call result, z2 sum
	g.layout = abi.EntryLayout{Arguments: []abi.ValueLocation{registerLoc("r0")}, Result: registerLoc("r0")}
	g.ops = [][]Operand{{{Value: 0}, {Value: 1, Def: true}}, {{Value: 0}, {Value: 1}, {Value: 2, Def: true}}, {{Value: 2}}}
	g.calls[0] = &abi.CallEffect{ArgumentLocations: []abi.ValueLocation{registerLoc("r0")}, ResultLocation: registerLoc("r0"),
		ClobberedRegisters: []string{"r0", "r1"}}
	g.returns[2] = true
	g.blocks = []Block{{Start: 0, End: 3, Params: []int{0}}}
	g.succs = [][]int{nil}
	w, err := recordWalk(g.config, g.function(), &Online{})
	if err != nil {
		t.Fatal(err)
	}
	runGenerated(t, g, w, 6)
	if err := Verify(g.config, g.function(), &Online{}); err != nil {
		t.Fatal(err)
	}
	for p := range w.before {
		for _, m := range append(w.before[p], w.after[p]...) {
			if m.kind != recMove {
				t.Errorf("position %d goes through memory: %+v", p, m)
			}
		}
	}
	if x := w.before[0]; len(x) != 2 || x[0].to.Registers[0] < 2 || x[1].to != SingleRegister(0) {
		t.Errorf("call moves %+v, want x to a kept register and a copy back to r0", x)
	}
}
