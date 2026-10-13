//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package compile_test

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// crossBlockShape is a random control-flow graph whose blocks use values
// from their dominators. Body block i takes a fuel parameter and extra
// parameters, computes, and leaves for the exit when fuel runs out or for
// its dispatch block otherwise. Dispatch block i, reached only from body i,
// uses body i's values by dominance and branches to two bodies. Fuel bounds
// every execution.
type crossBlockShape struct {
	r      *rand.Rand
	pb     *ir.ProgramBuilder
	bodies []ir.Block
	extra  []int // extra parameters of each body
	exit   ir.Block
	// evaluation model
	blocks map[ir.Block]*modelBlock
}

type modelOp struct {
	id   int
	op   ir.Opcode
	a, b int
	imm  uint64
}

type modelEdge struct {
	to   ir.Block
	args []int
}

type modelBlock struct {
	params []int
	ops    []modelOp
	// terminator: a zero-fuel test (body), a comparison (dispatch), a jump or a return
	kind        string
	fuel        int // body: fuel parameter; dispatch: next fuel value
	left, right int
	edges       [2]modelEdge
	result      int
}

func (s *crossBlockShape) append(b ir.Block, m *modelBlock, op ir.Opcode, a, c int, imm uint64) int {
	ins := ir.Instruction{Op: op, A: a, B: c, Imm: imm}
	if op == ir.OpConst {
		ins.A, ins.B = 0, 0
	}
	id := s.pb.Append(b, ins)
	m.ops = append(m.ops, modelOp{id: id, op: op, a: a, b: c, imm: imm})
	return id
}

// compute appends n random operations over pool, returning the grown pool.
func (s *crossBlockShape) compute(b ir.Block, m *modelBlock, pool []int, n int) []int {
	for range n {
		switch s.r.Intn(6) {
		case 0:
			pool = append(pool, s.append(b, m, ir.OpConst, 0, 0, uint64(s.r.Int63n(1000))))
		default:
			op := []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpXor, ir.OpMul, ir.OpOr}[s.r.Intn(5)]
			pool = append(pool, s.append(b, m, op, pool[s.r.Intn(len(pool))], pool[s.r.Intn(len(pool))], 0))
		}
	}
	return pool
}

func buildCrossBlockShape(seed int64) (*ir.SealedProgram, *crossBlockShape, error) {
	r := rand.New(rand.NewSource(seed))
	s := &crossBlockShape{r: r, pb: ir.NewProgramBuilder(), blocks: map[ir.Block]*modelBlock{}}
	word := ir.Scalar(64)
	n := 1 + r.Intn(6)
	for range n {
		k := r.Intn(3)
		types := make([]ir.Type, 1+k)
		for i := range types {
			types[i] = word
		}
		s.bodies = append(s.bodies, s.pb.NewBlock(types...))
		s.extra = append(s.extra, k)
	}
	dispatch := make([]ir.Block, n)
	for i := range dispatch {
		dispatch[i] = s.pb.NewBlock()
	}
	s.exit = s.pb.NewBlock(word)
	// Successors of each dispatch block, chosen before any code so that the
	// generator knows the graph's dominators.
	targets := make([][2]int, n)
	for i := range targets {
		targets[i] = [2]int{r.Intn(n), r.Intn(n)}
	}
	// Dominators over bodies: body 0 is entered from the entry block; body j
	// is entered from the dispatch blocks of the bodies that name it.
	preds := make([][]int, n)
	for i, t := range targets {
		for _, j := range t {
			preds[j] = append(preds[j], i)
		}
	}
	dom := make([][]bool, n) // dom[j][i]: body i dominates body j
	for j := range dom {
		dom[j] = make([]bool, n)
		for i := range dom[j] {
			dom[j][i] = j != 0 || i == 0
		}
	}
	for changed := true; changed; {
		changed = false
		for j := 1; j < n; j++ {
			next := make([]bool, n)
			for i := range next {
				next[i] = len(preds[j]) != 0
			}
			for _, p := range preds[j] {
				for i := range next {
					next[i] = next[i] && dom[p][i]
				}
			}
			next[j] = true
			for i := range next {
				if next[i] != dom[j][i] {
					dom[j][i], changed = next[i], true
				}
			}
		}
	}
	// Reachability from body 0, so that unreachable bodies use only entry values.
	reach := make([]bool, n)
	reach[0] = true
	for work := []int{0}; len(work) != 0; {
		i := work[len(work)-1]
		work = work[:len(work)-1]
		for _, j := range targets[i] {
			if !reach[j] {
				reach[j] = true
				work = append(work, j)
			}
		}
	}
	// Entry: two parameters and some values every block may use.
	entry := s.pb.Entry()
	em := &modelBlock{}
	s.blocks[entry] = em
	x := s.pb.Param(entry, word)
	y := s.pb.Param(entry, word)
	em.params = []int{x, y}
	entryValues := s.compute(entry, em, []int{x, y}, 1+r.Intn(4))
	fuel := s.append(entry, em, ir.OpConst, 0, 0, uint64(5+r.Intn(20)))
	// Values defined by each body and its dispatch block.
	defined := make([][]int, n)
	visible := func(j int) []int {
		pool := append([]int(nil), entryValues...)
		for i := range n {
			if i != j && reach[j] && dom[j][i] {
				pool = append(pool, defined[i]...)
			}
		}
		return pool
	}
	// Bodies in an order where dominators come first: dominator count.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	count := func(j int) int {
		c := 0
		for i := range n {
			if dom[j][i] {
				c++
			}
		}
		return c
	}
	for i := range order {
		for k := i + 1; k < len(order); k++ {
			if count(order[k]) < count(order[i]) {
				order[i], order[k] = order[k], order[i]
			}
		}
	}
	bodyFuel := make([]int, n)
	for _, i := range order {
		b := s.bodies[i]
		m := &modelBlock{kind: "body"}
		s.blocks[b] = m
		for p := range 1 + s.extra[i] {
			m.params = append(m.params, s.pb.BlockParam(b, p))
		}
		bodyFuel[i] = m.params[0]
		pool := append(visible(i), m.params[1:]...)
		pool = s.compute(b, m, pool, 1+r.Intn(5))
		defined[i] = append(append([]int(nil), m.params...), pool[len(visible(i))+len(m.params)-1:]...)
		m.fuel = m.params[0]
		m.result = pool[r.Intn(len(pool))]
		m.edges = [2]modelEdge{{to: s.exit, args: []int{m.result}}, {to: dispatch[i]}}
		s.pb.Branch(b, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: m.fuel},
			s.exit, []int{m.result}, dispatch[i], nil)
	}
	for i := range n {
		b := dispatch[i]
		m := &modelBlock{kind: "dispatch"}
		s.blocks[b] = m
		pool := append(visible(i), defined[i]...)
		pool = s.compute(b, m, pool, r.Intn(3))
		one := s.append(b, m, ir.OpConst, 0, 0, 1)
		m.fuel = s.append(b, m, ir.OpSub, bodyFuel[i], one, 0)
		m.left, m.right = pool[r.Intn(len(pool))], pool[r.Intn(len(pool))]
		for e, j := range targets[i] {
			args := []int{m.fuel}
			for range s.extra[j] {
				args = append(args, pool[r.Intn(len(pool))])
			}
			m.edges[e] = modelEdge{to: s.bodies[j], args: args}
		}
		s.pb.Branch(b, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchBelow, Value: m.left, Other: m.right, WithValue: true},
			m.edges[0].to, m.edges[0].args, m.edges[1].to, m.edges[1].args)
	}
	em.kind = "jump"
	em.edges[0] = modelEdge{to: s.bodies[0], args: append([]int{fuel}, entryValues[:s.extra[0]]...)}
	s.pb.Jump(entry, em.edges[0].to, em.edges[0].args...)
	xm := &modelBlock{kind: "return", params: []int{s.pb.BlockParam(s.exit, 0)}}
	s.blocks[s.exit] = xm
	pool := append(append(visible(0), defined[0]...), xm.params...) // body 0 dominates the exit
	pool = s.compute(s.exit, xm, pool, 1+r.Intn(3))
	xm.result = pool[len(pool)-1]
	s.pb.Return(s.exit, xm.result)
	program, err := s.pb.Seal()
	return program, s, err
}

func (s *crossBlockShape) evaluate(x, y uint64) uint64 {
	values := map[int]uint64{}
	block, args := ir.Block(0), []uint64{x, y}
	for {
		m := s.blocks[block]
		for i, p := range m.params {
			values[p] = args[i]
		}
		for _, op := range m.ops {
			a, b := values[op.a], values[op.b]
			switch op.op {
			case ir.OpConst:
				values[op.id] = op.imm
			case ir.OpAdd:
				values[op.id] = a + b
			case ir.OpSub:
				values[op.id] = a - b
			case ir.OpXor:
				values[op.id] = a ^ b
			case ir.OpMul:
				values[op.id] = a * b
			case ir.OpOr:
				values[op.id] = a | b
			}
		}
		edge := m.edges[0]
		switch m.kind {
		case "return":
			return values[m.result]
		case "body":
			if values[m.fuel] != 0 {
				edge = m.edges[1]
			}
		case "dispatch":
			if !(values[m.left] < values[m.right]) {
				edge = m.edges[1]
			}
		}
		args = args[:0]
		for _, id := range edge.args {
			args = append(args, values[id])
		}
		block = edge.to
	}
}

func TestCrossBlockRandomPrograms(t *testing.T) {
	count := 300
	if s := os.Getenv("NYANJIT_CROSS_BLOCK_PROGRAMS"); s != "" {
		count, _ = strconv.Atoi(s)
	}
	funcs := map[string]compile.Func{
		"plain":    {ABI: abi.C},
		"hardened": {ABI: abi.C, Harden: true},
		"stack":    {ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}},
	}
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		t.Fatal(err)
	}
	inputs := [][2]uint64{{0, 0}, {3, 7}, {1 << 40, 5}, {^uint64(0), 1}}
	limited := 0
	defer func() { t.Logf("%d of %d compilations hit a backend limit", limited, count*len(funcs)) }()
	for seed := range int64(count) {
		program, shape, err := buildCrossBlockShape(seed)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for name, f := range funcs {
			unit, err := compile.Lower(target, program, f)
			if name == "hardened" && errors.Is(err, compile.ErrUnsupported) {
				continue // some targets cannot harden control flow
			}
			if err != nil {
				t.Fatalf("seed %d %s: %v", seed, name, err)
			}
			tmpl, err := unit.Template()
			if err != nil {
				t.Fatalf("seed %d %s: %v", seed, name, err)
			}
			code, err := tmpl.Bind(nil)
			if errors.Is(err, compile.ErrBackendLimit) {
				limited++ // a large frame for the platform stack
				continue
			}
			if err != nil {
				t.Fatalf("seed %d %s: %v", seed, name, err)
			}
			for _, in := range inputs {
				lo, hi, err := code.CallPair(execution.Words(in[0], in[1])...)
				got, want := execution.Wide(lo, hi), shape.evaluate(in[0], in[1])
				if err != nil || got != want {
					listing, _ := unit.Listing()
					code.Close()
					t.Fatalf("seed %d %s: f(%d, %d) = %d, %v; want %d\n%s", seed, name, in[0], in[1], got, err, want, fmt.Sprint(listing))
				}
			}
			code.Close()
		}
	}
}
