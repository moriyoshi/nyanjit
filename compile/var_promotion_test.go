package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

// promotionPolicies are the policies the pass runs with: the per-block
// allocator's and the Online allocator's.
var promotionPolicies = map[string]promotionPolicy{
	"block":  {blockCapacity: 1},
	"online": {loopCarried: true, capacity: [2]int{24, 0}},
	"all":    {loopCarried: true, capacity: [2]int{1 << 20, 0}},
}

// checkPromotion promotes p under every policy, checks that the promoted
// and threaded programs validate and compute what p computes on inputs, and
// returns the number of Vars each policy keeps in homes.
func checkPromotion(t *testing.T, p *ir.Program, inputs [][]uint64) map[string]int {
	t.Helper()
	withVarPromotion(t)
	sealed, err := ir.Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	view, facts, err := validatedSource(sealed)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]int{}
	for name, policy := range promotionPolicies {
		promoted := promoteVariables(view, facts, policy)
		kept[name] = len(promoted.Variables)
		candidates := []*ir.Program{promoted}
		if hasCrossBlockUses(promoted) {
			threaded, err := threadCrossBlockValues(promoted)
			if err != nil {
				t.Fatalf("%s: threading: %v", name, err)
			}
			candidates = append(candidates, threaded)
		}
		for _, c := range candidates {
			got, err := ir.Seal(c)
			if err != nil {
				t.Fatalf("%s: promoted program does not validate: %v", name, err)
			}
			for _, in := range inputs {
				want, err := irinterp.Run(sealed, in, nil)
				if err != nil {
					t.Fatal(err)
				}
				have, err := irinterp.Run(got, in, nil)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if want.Lo != have.Lo || want.Hi != have.Hi {
					t.Fatalf("%s: inputs %v: promoted %#x:%#x, source %#x:%#x", name, in, have.Hi, have.Lo, want.Hi, want.Lo)
				}
			}
		}
	}
	return kept
}

func TestPromoteDiamond(t *testing.T) {
	b := builder.NewBuilder()
	a, k := must(b.Param()), must(b.Param())
	x := must(b.Var(must(b.Const(0))))
	yes, done := b.NewLabel(), b.NewLabel()
	mustDo(b.Branch(must(b.Compare(a, ir.BranchLess, k)), yes))
	mustDo(b.Set(x, must(b.Mul(a, k))))
	mustDo(b.Jump(done))
	mustDo(b.Bind(yes))
	mustDo(b.Set(x, must(b.Add(a, k))))
	mustDo(b.Bind(done))
	mustDo(b.Return(must(b.Get(x))))
	kept := checkPromotion(t, codegenFinish(t, b), [][]uint64{{1, 2}, {5, 3}, {7, 7}})
	for name, n := range kept {
		if n != 0 {
			t.Errorf("%s: the merge Var stays in its home", name)
		}
	}
}

func TestPromoteLoop(t *testing.T) {
	b := builder.NewBuilder()
	n := must(b.Param())
	i, acc := must(b.Var(must(b.Const(0)))), must(b.Var(must(b.Const(0))))
	loop, done := b.NewLabel(), b.NewLabel()
	mustDo(b.Bind(loop))
	mustDo(b.Branch(must(b.Compare(must(b.Get(i)), ir.BranchAboveEqual, n)), done))
	mustDo(b.Set(acc, must(b.Add(must(b.Get(acc)), must(b.Mul(must(b.Get(i)), must(b.Get(i))))))))
	mustDo(b.Set(i, must(b.Add(must(b.Get(i)), must(b.Const(1))))))
	mustDo(b.Jump(loop))
	mustDo(b.Bind(done))
	mustDo(b.Return(must(b.Get(acc))))
	kept := checkPromotion(t, codegenFinish(t, b), [][]uint64{{0}, {1}, {10}})
	if kept["block"] != 2 || kept["online"] != 0 {
		t.Errorf("loop-carried Vars: kept %v, want both in homes for the per-block allocator and none for Online", kept)
	}
}

// A Get is a snapshot: a later Set does not change it, on every path.
func TestPromoteSnapshot(t *testing.T) {
	b := builder.NewBuilder()
	a := must(b.Param())
	x := must(b.Var(a))
	old := must(b.Get(x))
	mustDo(b.Set(x, must(b.Add(old, must(b.Const(5))))))
	skip, done := b.NewLabel(), b.NewLabel()
	mustDo(b.Branch(must(b.IsZero(a)), skip))
	mustDo(b.Set(x, must(b.Mul(must(b.Get(x)), old))))
	mustDo(b.Jump(done))
	mustDo(b.Bind(skip))
	mustDo(b.Jump(done))
	mustDo(b.Bind(done))
	mustDo(b.Return(must(b.Xor(must(b.Get(x)), old))))
	checkPromotion(t, codegenFinish(t, b), [][]uint64{{0}, {3}, {1 << 40}})
}

func TestPromoteWideVar(t *testing.T) {
	b := builder.NewBuilder()
	x, y := must(b.Param()), must(b.Param())
	v := must(b.Var(must(b.Concat(x, y))))
	other, done := b.NewLabel(), b.NewLabel()
	mustDo(b.Branch(must(b.Compare(x, ir.BranchBelow, y)), other))
	mustDo(b.Set(v, must(b.Concat(must(b.Add(x, y)), must(b.Xor(x, y))))))
	mustDo(b.Jump(done))
	mustDo(b.Bind(other))
	mustDo(b.Set(v, must(b.Concat(y, x))))
	mustDo(b.Bind(done))
	got := must(b.Get(v))
	mustDo(b.Return(must(b.Xor(must(b.ExtractBits(got, 0, 64)), must(b.ExtractBits(got, 64, 64))))))
	checkPromotion(t, codegenFinish(t, b), [][]uint64{{1, 2}, {9, 3}})
}

// Captures and Vars of nested scopes, an outer Var changed inside a scope
// and read after it, and a scope export.
func TestPromoteScopes(t *testing.T) {
	b := builder.NewBuilder()
	x, y := must(b.Param()), must(b.Param())
	outer := must(b.Var(x))
	shifted := must(b.Add(x, must(b.Const(2))))
	mustDo(b.Enter())
	inner := must(b.Var(must(b.Mul(shifted, y))))
	k := must(b.Var(must(b.Const(3))))
	loop, done := b.NewLabel(), b.NewLabel()
	mustDo(b.Jump(loop))
	mustDo(b.Bind(loop))
	mustDo(b.Branch(must(b.IsZero(must(b.Get(k)))), done))
	mustDo(b.Set(inner, must(b.Xor(must(b.Get(inner)), must(b.Add(shifted, must(b.Get(k))))))))
	mustDo(b.Set(outer, must(b.Add(must(b.Get(outer)), must(b.Get(k))))))
	mustDo(b.Set(k, must(b.Sub(must(b.Get(k)), must(b.Const(1))))))
	mustDo(b.Jump(loop))
	mustDo(b.Bind(done))
	result := must(b.Get(inner))
	mustDo(b.Leave(result))
	mustDo(b.Return(must(b.Add(result, must(b.Get(outer))))))
	p := codegenFinish(t, b)
	captures := 0
	for _, v := range p.Variables {
		if v.Immutable {
			captures++
		}
	}
	if captures == 0 {
		t.Fatal("the program has no capture to promote")
	}
	checkPromotion(t, p, [][]uint64{{1, 2}, {4, 9}})
}

// A Var accessed in a block no path reaches is promoted; that block's code
// never runs.
func TestPromoteUnreachable(t *testing.T) {
	b := builder.NewBuilder()
	a := must(b.Param())
	x := must(b.Var(a))
	dead, done := b.NewLabel(), b.NewLabel()
	mustDo(b.Set(x, must(b.Add(a, must(b.Const(1))))))
	mustDo(b.Jump(done))
	mustDo(b.Bind(dead))
	mustDo(b.Set(x, must(b.Mul(must(b.Get(x)), a))))
	mustDo(b.Jump(done))
	mustDo(b.Bind(done))
	mustDo(b.Return(must(b.Get(x))))
	checkPromotion(t, codegenFinish(t, b), [][]uint64{{0}, {6}})
}

// A Set in a block laid out after the block that reads it, which it
// dominates, would read a later value: the Var keeps its home.
func TestPromoteLayoutOrder(t *testing.T) {
	b := builder.NewBuilder()
	a := must(b.Param())
	x := must(b.Var(a))
	read, write := b.NewLabel(), b.NewLabel()
	mustDo(b.Jump(write))
	mustDo(b.Bind(read))
	mustDo(b.Return(must(b.Get(x))))
	mustDo(b.Bind(write))
	mustDo(b.Set(x, must(b.Add(a, must(b.Const(7))))))
	mustDo(b.Jump(read))
	kept := checkPromotion(t, codegenFinish(t, b), [][]uint64{{0}, {5}})
	for name, n := range kept {
		if n != 1 {
			t.Errorf("%s: kept %d Vars, want the one read before its definition in layout", name, n)
		}
	}
}

func TestPromoteIrreducible(t *testing.T) {
	b := builder.NewBuilder()
	x := must(b.Param())
	v, r := must(b.Var(x)), must(b.Var(must(b.Const(0))))
	first, second, exit := b.NewLabel(), b.NewLabel(), b.NewLabel()
	mustDo(b.Branch(must(b.IsZero(must(b.And(x, must(b.Const(1)))))), first))
	mustDo(b.Jump(second))
	mustDo(b.Bind(first))
	a := must(b.Get(v))
	mustDo(b.Set(r, must(b.Mul(a, must(b.Const(7))))))
	mustDo(b.Branch(must(b.CompareImmediate(a, ir.BranchBelow, 4)), exit))
	mustDo(b.Set(v, must(b.Sub(a, must(b.Const(3))))))
	mustDo(b.Bind(second))
	c := must(b.Get(v))
	mustDo(b.Set(r, must(b.Mul(c, must(b.Const(11))))))
	mustDo(b.Branch(must(b.CompareImmediate(c, ir.BranchBelow, 5)), exit))
	mustDo(b.Set(v, must(b.ShiftRight(c, 1))))
	mustDo(b.Jump(first))
	mustDo(b.Bind(exit))
	mustDo(b.Return(must(b.Get(r))))
	checkPromotion(t, codegenFinish(t, b), [][]uint64{{0}, {1}, {40}, {1001}})
}

// The empty fallthrough of Branch followed by Jump is folded into the
// branch, which then passes the merge's parameter itself.
func TestPromoteFoldsEmptyJump(t *testing.T) {
	b := builder.NewBuilder()
	a := must(b.Param())
	x := must(b.Var(must(b.Const(1))))
	// The Builder merges a Jump right after a Branch into a two-way branch,
	// so bind the fallthrough explicitly to leave a block holding only a jump.
	loop, back, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	mustDo(b.Bind(loop))
	next := must(b.Add(must(b.Get(x)), a))
	mustDo(b.Set(x, next))
	mustDo(b.Branch(must(b.CompareImmediate(next, ir.BranchAbove, 100)), done))
	mustDo(b.Bind(back))
	mustDo(b.Jump(loop))
	mustDo(b.Bind(done))
	mustDo(b.Return(must(b.Get(x))))
	p := codegenFinish(t, b)
	checkPromotion(t, p, [][]uint64{{1}, {30}, {200}})
	withVarPromotion(t)
	sealed, _ := ir.Seal(p)
	view, facts, _ := validatedSource(sealed)
	promoted := promoteVariables(view, facts, promotionPolicies["online"])
	segments := func(c *ir.Control) (n int) {
		for _, s := range c.Steps {
			if !s.IsLabel {
				n++
			}
		}
		return n
	}
	if got, was := segments(promoted.Control), segments(p.Control); got != was-1 {
		t.Errorf("segments %d, want %d", got, was-1)
	}
}

// A Var whose Get feeds a CallGo target stays in its home, so the target
// proof sees the same Get as without promotion.
func TestPromoteKeepsCallGoTarget(t *testing.T) {
	b := builder.NewBuilder()
	x := must(b.Param())
	target := must(b.ConstNative(0x1234))
	mustDo(b.Enter())
	loop := b.NewLabel()
	mustDo(b.Jump(loop))
	mustDo(b.Bind(loop))
	r := must(b.CallGo(target, x))
	mustDo(b.Leave(r))
	mustDo(b.Return(r))
	p := codegenFinish(t, b)
	sealed, err := ir.Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	withVarPromotion(t)
	view, facts, _ := validatedSource(sealed)
	for name, policy := range promotionPolicies {
		promoted := promoteVariables(view, facts, policy)
		kept := false
		for _, v := range promoted.Variables {
			kept = kept || v.Immutable && promoted.Instructions[v.Initial[0]].Op == ir.OpConst
		}
		if !kept || len(promoted.Variables) != 1 {
			t.Errorf("%s: kept %+v, want only the capture of the CallGo target", name, promoted.Variables)
		}
	}
}

func TestPromoteVectorVar(t *testing.T) {
	b := builder.NewBuilder()
	x := must(b.Param())
	t4 := ir.Vec(2, 64)
	v := must(b.Var(must(b.Splat(t4, x))))
	mustDo(b.Set(v, must(b.Add(must(b.Get(v)), must(b.Get(v))))))
	mustDo(b.Return(must(b.ExtractLane(must(b.Get(v)), 1))))
	p := codegenFinish(t, b)
	kept := checkPromotion(t, p, [][]uint64{{3}, {1 << 62}})
	for name, n := range kept {
		if n != 0 {
			t.Errorf("%s: straight-line vector Var stays in its home", name)
		}
	}
}
