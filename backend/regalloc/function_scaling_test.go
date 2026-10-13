package regalloc

import (
	"math"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// scalingShape builds a Function of size n with eight registers.
type scalingShape struct {
	name  string
	build func(n int) (*genFunction, Function)
}

func scalingShapes() []scalingShape {
	return []scalingShape{
		{"straight", straightFunction},
		{"blocks", chainFunction},
		{"loop", loopFunction},
	}
}

func newScaling(registers int) *genFunction {
	g := &genFunction{constants: map[int]uint64{}, calls: map[int]*abi.CallEffect{}, returns: map[int]bool{}}
	g.config = Config{Registers: named(gprs(registers), names(registers)), SlotLimit: 1 << 30}
	g.layout.Result = registerLoc("r0")
	return g
}

// straightFunction is one block of n positions, each reading the previous
// value and one eight positions back.
func straightFunction(n int) (*genFunction, Function) {
	g := newScaling(8)
	g.values = make([]Value, n)
	for i := range n {
		ops := []Operand{{Value: i, Def: true}}
		if i > 0 {
			ops = append(ops, Operand{Value: i - 1})
		}
		if i >= 8 {
			ops = append(ops, Operand{Value: i - 8})
		}
		g.ops = append(g.ops, ops)
	}
	g.ops = append(g.ops, []Operand{{Value: n - 1}})
	g.returns[n] = true
	g.blocks = []Block{{Start: 0, End: n + 1}}
	g.succs = [][]int{nil}
	return g, g.function()
}

// chainFunction is n blocks passing two parameters along a chain, each also
// branching to the exit, with four entry values live throughout.
func chainFunction(n int) (*genFunction, Function) {
	g := newScaling(8)
	value := func() int { g.values = append(g.values, Value{}); return len(g.values) - 1 }
	var entry []int
	for range 4 {
		entry = append(entry, value())
	}
	g.layout.Arguments = []abi.ValueLocation{registerLoc("r0"), registerLoc("r1"), registerLoc("r2"), registerLoc("r3")}
	g.blocks = append(g.blocks, Block{Start: 0, End: 1, Params: entry})
	g.ops = append(g.ops, []Operand{{Value: entry[0]}})
	carried := []int{entry[0], entry[1]}
	exit := n + 1
	g.succs = make([][]int, n+2)
	addEdge := func(from, to int, args []int) {
		g.edges = append(g.edges, Edge{From: from, To: to, Args: args})
		g.succs[from] = append(g.succs[from], len(g.edges)-1)
	}
	for b := 1; b <= n; b++ {
		params := []int{value(), value()}
		addEdge(b-1, b, carried)
		sum := value()
		start := len(g.ops)
		g.ops = append(g.ops, []Operand{{Value: params[0]}, {Value: params[1]}, {Value: entry[2+b%2]}, {Value: sum, Def: true}})
		g.ops = append(g.ops, []Operand{{Value: sum}})
		g.blocks = append(g.blocks, Block{Start: start, End: len(g.ops), Params: params})
		addEdge(b-1, exit, nil)
		carried = []int{sum, params[0]}
	}
	addEdge(n, exit, nil)
	start := len(g.ops)
	g.ops = append(g.ops, []Operand{{Value: entry[3]}})
	g.returns[start] = true
	g.blocks = append(g.blocks, Block{Start: start, End: start + 1})
	return g, g.function()
}

// loopFunction keeps sixteen entry values live across a loop of n positions
// that reads them in turn.
func loopFunction(n int) (*genFunction, Function) {
	g := newScaling(8)
	const live = 16
	g.values = make([]Value, live)
	var defs []Operand
	for v := range live {
		defs = append(defs, Operand{Value: v, Def: true})
	}
	g.ops = append(g.ops, defs[:8], defs[8:])
	g.blocks = append(g.blocks, Block{Start: 0, End: 2})
	start := len(g.ops)
	previous := -1
	for i := range n {
		v := len(g.values)
		g.values = append(g.values, Value{})
		ops := []Operand{{Value: i % live}, {Value: v, Def: true}}
		if previous >= 0 {
			ops = append(ops, Operand{Value: previous})
		}
		g.ops = append(g.ops, ops)
		previous = v
	}
	g.ops = append(g.ops, []Operand{{Value: previous}})
	g.blocks = append(g.blocks, Block{Start: start, End: len(g.ops)})
	exit := len(g.ops)
	g.ops = append(g.ops, []Operand{{Value: 3}})
	g.returns[exit] = true
	g.blocks = append(g.blocks, Block{Start: exit, End: exit + 1})
	g.edges = []Edge{{From: 0, To: 1}, {From: 1, To: 1}, {From: 1, To: 2}}
	g.succs = [][]int{{0}, {1, 2}, nil}
	return g, g.function()
}

func TestBatchScalingShapesRun(t *testing.T) {
	var a Batch
	for _, shape := range scalingShapes() {
		g, f := shape.build(200)
		s, err := recordWalk(g.config, f, &a)
		if err != nil {
			t.Fatalf("%s: %v", shape.name, err)
		}
		runGenerated(t, g, s, 3)
		a.Finish()
	}
}

// TestBatchScaling fits the per-doubling growth of Allocate over
// four sizes spanning 8x; it must stay within the scaling gate's 2.3x.
func TestBatchScaling(t *testing.T) {
	if os.Getenv("NYANJIT_PERF_SCALING") != "1" {
		t.Skip("set NYANJIT_PERF_SCALING=1 to run the native timing gate")
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, shape := range scalingShapes() {
		t.Run(shape.name, func(t *testing.T) {
			sizes := [4]int{1000, 2000, 4000, 8000}
			var a Batch
			measure := func(n int) float64 {
				g, f := shape.build(n)
				if err := a.Allocate(g.config, f, nil); err != nil {
					t.Fatal(err)
				}
				a.Finish()
				start := time.Now()
				runs := 0
				for runs == 0 || time.Since(start) < 100*time.Millisecond {
					if err := a.Allocate(g.config, f, nil); err != nil {
						t.Fatal(err)
					}
					a.Finish()
					runs++
				}
				return float64(time.Since(start).Nanoseconds()) / float64(runs)
			}
			growth := make([]float64, 5)
			var times [4]float64
			for run := range growth {
				runtime.GC()
				for i, n := range sizes {
					times[i] = measure(n)
				}
				slope := 0.0
				for i, elapsed := range times {
					slope += (float64(i) - 1.5) * math.Log2(elapsed)
				}
				growth[run] = math.Exp2(slope / 5)
			}
			slices.Sort(growth)
			median := growth[len(growth)/2]
			t.Logf("n%v last=%.3fms/%.3fms/%.3fms/%.3fms fitted doubling=%.3fx", sizes,
				times[0]/1e6, times[1]/1e6, times[2]/1e6, times[3]/1e6, median)
			if median > 2.3 {
				t.Fatalf("scaling regression: fitted per-doubling growth %.3fx (limit 2.3x)", median)
			}
		})
	}
}

func BenchmarkBatch(b *testing.B) {
	for _, shape := range scalingShapes() {
		b.Run(shape.name, func(b *testing.B) {
			g, f := shape.build(4000)
			var a Batch
			b.ReportAllocs()
			for b.Loop() {
				if err := a.Allocate(g.config, f, nil); err != nil {
					b.Fatal(err)
				}
				a.Finish()
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(g.ops)), "ns/position")
		})
	}
}
