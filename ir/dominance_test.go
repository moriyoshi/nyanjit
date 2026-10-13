package ir

import (
	"errors"
	"math/rand"
	"testing"
)

// graphFromEdges builds a blockGraph with one single-instruction block per
// node.
func graphFromEdges(n int, edges [][2]int) *blockGraph {
	g := &blockGraph{steps: make([]int, n), ends: make([]int, n), succStart: make([]int, n+1), predStart: make([]int, n+2)}
	for b := range n {
		g.steps[b], g.ends[b] = b, b+1
	}
	for _, e := range edges {
		g.succStart[e[0]+1]++
		g.predStart[e[1]+2]++
	}
	for b := 0; b < n; b++ {
		g.succStart[b+1] += g.succStart[b]
		g.predStart[b+2] += g.predStart[b+1]
	}
	g.succ, g.pred = make([]int, len(edges)), make([]int, len(edges))
	fill := append([]int(nil), g.succStart...)
	for _, e := range edges {
		g.succ[fill[e[0]]] = e[1]
		fill[e[0]]++
		g.pred[g.predStart[e[1]+1]] = e[0]
		g.predStart[e[1]+1]++
	}
	return g
}

// naiveDominators solves the dominator data-flow equations directly.
func naiveDominators(g *blockGraph) [][]bool {
	n := len(g.steps)
	reach := make([]bool, n)
	reach[0] = true
	for work := []int{0}; len(work) != 0; {
		b := work[len(work)-1]
		work = work[:len(work)-1]
		for _, s := range g.successors(b) {
			if !reach[s] {
				reach[s] = true
				work = append(work, s)
			}
		}
	}
	dom := make([][]bool, n)
	for b := range dom {
		dom[b] = make([]bool, n)
		for a := range dom[b] {
			dom[b][a] = b != 0 || a == 0
		}
	}
	for changed := true; changed; {
		changed = false
		for b := 1; b < n; b++ {
			if !reach[b] {
				continue
			}
			next := make([]bool, n)
			for a := range next {
				next[a] = true
			}
			for _, p := range g.predecessors(b) {
				if !reach[p] {
					continue
				}
				for a := range next {
					next[a] = next[a] && dom[p][a]
				}
			}
			next[b] = true
			for a := range next {
				if next[a] != dom[b][a] {
					dom[b][a] = next[a]
					changed = true
				}
			}
		}
	}
	return dom
}

func TestDominanceMatchesDataFlow(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for round := range 2000 {
		n := 1 + r.Intn(24)
		var edges [][2]int
		for range r.Intn(3 * n) {
			edges = append(edges, [2]int{r.Intn(n), r.Intn(n)})
		}
		// Mostly forward chains, so most blocks are reachable.
		for b := 1; b < n; b++ {
			if r.Intn(4) != 0 {
				edges = append(edges, [2]int{r.Intn(b), b})
			}
		}
		g := graphFromEdges(n, edges)
		d := newDominance(g)
		want := naiveDominators(g)
		for b := range n {
			for a := range n {
				if got := d.blockDominates(a, b); got != want[b][a] {
					t.Fatalf("round %d: blockDominates(%d, %d) = %v, want %v; edges %v", round, a, b, got, want[b][a], edges)
				}
			}
		}
	}
}

func TestDominanceDeepChain(t *testing.T) {
	const n = 200000
	edges := make([][2]int, 0, n)
	for b := 1; b < n; b++ {
		edges = append(edges, [2]int{b - 1, b})
	}
	edges = append(edges, [2]int{n - 1, 1})
	d := newDominance(graphFromEdges(n, edges))
	if !d.blockDominates(1, n-1) || d.blockDominates(n-1, 1) {
		t.Fatal("chain dominance is wrong")
	}
}

// diamond returns entry -> {left, right} -> join, where left defines value 2
// and the join returns the value given by use.
func diamond(use int) *Program {
	return &Program{
		Instructions: []Instruction{{Op: OpParam}, {Op: OpConst, Imm: 1}, {Op: OpAdd, A: 0, B: 1}, {Op: OpXor, A: 0, B: use}},
		Arity:        1, Result: -1,
		Control: &Control{Labels: []bool{true, true, true, true}, LabelScopes: make([]int, 4), Params: make([]int, 4), ParamBits: make([][]uint8, 4),
			Steps: []ControlStep{
				{IsLabel: true, Label: 0}, {Start: 0, End: 2, Arity: 1, Term: TermBranch, To: 1, Other: 2, Result: 0, Values: []int{0},
					Predicate: &PredicateExpr{Kind: PredicateCompare, Condition: BranchEqual, Value: 0}},
				{IsLabel: true, Label: 1}, {Start: 2, End: 3, Term: TermJump, To: 3},
				{IsLabel: true, Label: 2}, {Start: 3, End: 3, Term: TermJump, To: 3},
				{IsLabel: true, Label: 3}, {Start: 3, End: 4, Term: TermReturn, Result: 3},
			}},
	}
}

func TestValidateDominance(t *testing.T) {
	if err := diamond(1).Validate(); err != nil {
		t.Fatalf("value from the dominating entry block: %v", err)
	}
	err := diamond(2).Validate()
	var invalid *ValidationError
	if !errors.As(err, &invalid) || invalid.Index != 3 || invalid.Op != OpXor {
		t.Fatalf("value from a non-dominating arm: %v", err)
	}
	// A step operand from a non-dominating block names its step.
	p := diamond(1)
	p.Control.Steps[7].Result = 2
	if err := p.Validate(); !errors.As(err, &invalid) || invalid.Index != -1 {
		t.Fatalf("returned value from a non-dominating arm: %v", err)
	}
	// A reachable block cannot use a value from an unreachable one.
	p = diamond(2)
	p.Control.Steps[1] = ControlStep{Start: 0, End: 2, Arity: 1, Term: TermJump, To: 3}
	p.Control.Steps[3] = ControlStep{Start: 2, End: 3, Term: TermJump, To: 3}
	p.Control.Steps[5] = ControlStep{Start: 3, End: 3, Term: TermJump, To: 1}
	p.Control.Steps[7] = ControlStep{Start: 3, End: 4, Term: TermReturn, Result: 0}
	if err := p.Validate(); err == nil {
		t.Fatal("reachable join used a value from an unreachable block")
	}
}

func TestValidateRejectsMixedReturnWidths(t *testing.T) {
	p := &Program{
		Instructions: []Instruction{{Op: OpParam}, {Op: OpConst}, {Op: OpConst, Imm: 1}},
		Arity:        1, Result: -1,
		Control: &Control{Labels: []bool{true, true, true}, LabelScopes: make([]int, 3), Params: make([]int, 3), ParamBits: make([][]uint8, 3),
			Steps: []ControlStep{
				{IsLabel: true, Label: 0}, {Start: 0, End: 2, Arity: 1, Term: TermBranch, To: 1, Other: 2, Result: 0, Values: []int{0},
					Predicate: &PredicateExpr{Kind: PredicateCompare, Condition: BranchEqual, Value: 0}},
				{IsLabel: true, Label: 1}, {Start: 2, End: 3, Term: TermReturn, Result: 2, ResultParts: []int{2, 1}},
				{IsLabel: true, Label: 2}, {Start: 3, End: 3, Term: TermReturn, Result: 1},
			}},
	}
	if err := p.Validate(); !errors.Is(err, ErrInvalidProgram) {
		t.Fatalf("wide and scalar returns: %v", err)
	}
}

// TestValidateUnreachableUses checks that blocks unreachable from the entry
// may use any earlier value: every block dominates them.
func TestValidateUnreachableUses(t *testing.T) {
	p := &Program{
		Instructions: []Instruction{{Op: OpParam}, {Op: OpConst, Imm: 1}, {Op: OpAdd, A: 0, B: 1}, {Op: OpXor, A: 2, B: 0}},
		Arity:        1, Result: -1,
		Control: &Control{Labels: []bool{true, true, true, true}, LabelScopes: make([]int, 4), Params: make([]int, 4), ParamBits: make([][]uint8, 4),
			Steps: []ControlStep{
				{IsLabel: true, Label: 0}, {Start: 0, End: 2, Arity: 1, Term: TermJump, To: 3},
				{IsLabel: true, Label: 1}, {Start: 2, End: 3, Term: TermJump, To: 3},
				{IsLabel: true, Label: 2}, {Start: 3, End: 4, Term: TermJump, To: 3},
				{IsLabel: true, Label: 3}, {Start: 4, End: 4, Term: TermReturn, Result: 0},
			}},
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

// Labels bound before the same segment alias one block: its OpLabelParam
// reads one parameter list, so the labels must declare the same parameters,
// and an edge to any of them enters the block.
func TestValidateAliasedLabelParameters(t *testing.T) {
	aliased := func(second []uint8) *Program {
		return &Program{
			Instructions: []Instruction{{Op: OpParam}, {Op: OpLabelParam, Imm: 0}},
			Arity:        1, Result: -1,
			Control: &Control{Labels: []bool{true, true, true}, LabelScopes: make([]int, 3),
				Params: []int{0, 1, len(second)}, ParamBits: [][]uint8{nil, {64}, second},
				Steps: []ControlStep{
					{IsLabel: true, Label: 0}, {Start: 0, End: 1, Arity: 1, Term: TermJump, To: 1, Args: []int{0}},
					{IsLabel: true, Label: 1}, {IsLabel: true, Label: 2}, {Start: 1, End: 2, Term: TermReturn, Result: 1},
				}},
		}
	}
	if err := aliased([]uint8{64}).Validate(); err != nil {
		t.Fatalf("alias without its own incoming edge: %v", err)
	}
	for _, second := range [][]uint8{{32}, nil, {64, 64}} {
		if err := aliased(second).Validate(); !errors.Is(err, ErrInvalidProgram) {
			t.Fatalf("aliased labels with parameters %v and %v: %v", []uint8{64}, second, err)
		}
	}
}

// ResultParts are the two 64-bit words of a 128-bit integer result; a vector
// or mask of the same lane width is not one (security audit 2026-10-25, F18).
func TestValidateResultPartsRequireScalars(t *testing.T) {
	for _, part := range []Instruction{{Op: OpVSplat, Type: Vec(2, 64), Bits: 64, A: 0}, {Op: OpVCompare, Type: Mask(2), Bits: 64, A: 1, B: 1, Imm: uint64(BranchEqual)}} {
		straight := &Program{Instructions: []Instruction{{Op: OpParam}, {Op: OpVSplat, Type: Vec(2, 64), Bits: 64, A: 0}, part}, Arity: 1, Result: 0, ResultParts: []int{0, 2}}
		if err := straight.Validate(); !errors.Is(err, ErrInvalidProgram) {
			t.Errorf("straight-line %v ResultParts: %v", part.Type, err)
		}
		control := &Program{
			Instructions: []Instruction{{Op: OpParam}, {Op: OpVSplat, Type: Vec(2, 64), Bits: 64, A: 0}, part}, Arity: 1, Result: -1,
			Control: &Control{Labels: []bool{true}, LabelScopes: make([]int, 1), Params: make([]int, 1), ParamBits: make([][]uint8, 1),
				Steps: []ControlStep{{IsLabel: true, Label: 0}, {Start: 0, End: 3, Arity: 1, Term: TermReturn, Result: 0, ResultParts: []int{0, 2}}}},
		}
		if err := control.Validate(); !errors.Is(err, ErrInvalidProgram) {
			t.Errorf("control %v ResultParts: %v", part.Type, err)
		}
	}
}

// Equality and unsigned comparisons of a narrow operand agree on every target
// only for immediates in its zero-extended range; 386 compares a 32-bit
// truncation (security audit 2026-10-25, L17).
func TestValidateNarrowCompareImmediates(t *testing.T) {
	program := func(bits uint8, condition BranchCondition, immediate int64) *Program {
		value := Instruction{Op: OpCastUnsigned, Bits: bits, A: 0}
		return &Program{
			Instructions: []Instruction{{Op: OpParam}, value, {Op: OpConst, Imm: 1}, {Op: OpConst, Imm: 0}},
			Arity:        1, Result: -1,
			Control: &Control{Labels: []bool{true, true, true}, LabelScopes: make([]int, 3), Params: make([]int, 3), ParamBits: make([][]uint8, 3),
				Steps: []ControlStep{
					{IsLabel: true, Label: 0}, {Start: 0, End: 2, Arity: 1, Term: TermBranch, To: 1, Other: 2, Result: 1, Values: []int{1},
						Predicate: &PredicateExpr{Kind: PredicateCompare, Condition: condition, Value: 1, Immediate: immediate}},
					{IsLabel: true, Label: 1}, {Start: 2, End: 3, Term: TermReturn, Result: 2},
					{IsLabel: true, Label: 2}, {Start: 3, End: 4, Term: TermReturn, Result: 3},
				}},
		}
	}
	for _, tc := range []struct {
		bits      uint8
		condition BranchCondition
		immediate int64
		valid     bool
	}{
		{32, BranchBelowEqual, -127, false},
		{32, BranchEqual, 1 << 32, false},
		{8, BranchAbove, 256, false},
		{8, BranchNotEqual, -1, false},
		{32, BranchBelowEqual, 1<<32 - 1, true},
		{8, BranchEqual, 255, true},
		{32, BranchLess, -127, true}, // signed narrow comparisons are target-dependent already
		{64, BranchBelowEqual, -127, true},
	} {
		err := program(tc.bits, tc.condition, tc.immediate).Validate()
		if (err == nil) != tc.valid {
			t.Errorf("%d-bit condition %d immediate %d: %v, want valid=%v", tc.bits, tc.condition, tc.immediate, err, tc.valid)
		}
	}
}
