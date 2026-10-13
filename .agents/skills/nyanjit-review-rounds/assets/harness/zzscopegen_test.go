//go:build linux && arm64

package varfuzz

import (
	"math/rand"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// Scope/Var-heavy structured generator: deep nesting, shadowing Vars, many
// Vars per scope, loops inside scopes, Vars live across calls, FP Vars,
// exports across Leave.
type zzSG struct {
	r      *rand.Rand
	b      *n.Builder
	x      n.Value
	ptrV   n.Var
	ints   [][]n.Var // per scope level
	fps    [][]n.Var
	budget int
	calls  bool
}

func (g *zzSG) anyInt() n.Var {
	// Prefer innermost (shadowing) but reach outer levels.
	lvl := len(g.ints) - 1
	for lvl > 0 && (len(g.ints[lvl]) == 0 || g.r.Intn(3) == 0) {
		lvl--
	}
	for len(g.ints[lvl]) == 0 {
		lvl++
	}
	vs := g.ints[lvl]
	return vs[g.r.Intn(len(vs))]
}

func (g *zzSG) anyFP() (n.Var, bool) {
	var all []n.Var
	for _, l := range g.fps {
		all = append(all, l...)
	}
	if len(all) == 0 {
		return n.Var{}, false
	}
	if g.r.Intn(2) == 0 {
		return all[len(all)-1], true
	}
	return all[g.r.Intn(len(all))], true
}

func (g *zzSG) expr() n.Value {
	b := g.b
	a := b.Get(g.anyInt())
	switch g.r.Intn(7) {
	case 0:
		return b.Add(a, b.Const(uint64(g.r.Intn(100))))
	case 1:
		return b.Xor(a, b.Get(g.anyInt()))
	case 2:
		return b.Sub(a, b.Get(g.anyInt()))
	case 3:
		return b.Mul(a, b.Const(uint64(g.r.Intn(9)+1)))
	case 4:
		if g.calls {
			return b.Add(a, b.CallC(b.ConstNative(0x61000), b.Get(g.anyInt()), a))
		}
		return b.And(a, b.Get(g.anyInt()))
	case 5:
		return b.RotateLeft(a, uint8(g.r.Intn(63)+1))
	default:
		return b.Add(a, b.Get(g.anyInt()))
	}
}

func (g *zzSG) stmt(depth int) {
	b := g.b
	g.budget--
	switch k := g.r.Intn(12); {
	case k < 4 || g.budget <= 0:
		b.Set(g.anyInt(), g.expr())
	case k < 5:
		if f, ok := g.anyFP(); ok {
			b.Set(f, b.FAdd(b.Get(f), b.Float64(float64(g.r.Intn(10))+0.25)))
		} else {
			b.Set(g.anyInt(), g.expr())
		}
	case k < 6:
		if f, ok := g.anyFP(); ok {
			b.StoreBits(b.Get(g.ptrV), b.Get(f))
		}
	case k < 8 && depth < 7:
		g.scope(depth+1, false)
	case k < 10 && depth < 7:
		g.scope(depth+1, true)
	case k < 11:
		// diamond
		t, e, j := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Branch(b.Compare(b.Get(g.anyInt()), ir.BranchEqual, b.Get(g.anyInt())), t)
		b.Jump(e)
		b.Bind(t)
		b.Set(g.anyInt(), g.expr())
		b.Jump(j)
		b.Bind(e)
		b.Set(g.anyInt(), g.expr())
		b.Jump(j)
		b.Bind(j)
	default:
		v := g.anyInt()
		b.Set(v, b.Add(b.Get(v), b.Get(g.anyInt())))
	}
}

func (g *zzSG) scope(depth int, loop bool) {
	b := g.b
	b.Enter()
	var ints, fps []n.Var
	nv := g.r.Intn(4)
	if g.r.Intn(8) == 0 {
		nv = 10 + g.r.Intn(50)
	}
	g.ints = append(g.ints, nil)
	g.fps = append(g.fps, nil)
	for i := 0; i < nv; i++ {
		// shadow: initialize from an outer var
		ints = append(ints, b.Var(b.Add(b.Get(g.anyInt()), b.Const(uint64(i)))))
		g.ints[len(g.ints)-1] = ints
	}
	if g.r.Intn(3) == 0 {
		fps = append(fps, b.Var(b.Float64(float64(depth)+0.5)))
		if f, ok := g.anyFP(); ok && g.r.Intn(2) == 0 {
			fps = append(fps, b.Var(b.FMul(b.Get(f), b.Float64(1.5))))
		}
		g.fps[len(g.fps)-1] = fps
	}
	var k n.Var
	var head, body, done n.Label
	if loop {
		k = b.Var(b.Const(uint64(g.r.Intn(3) + 1)))
		head, body, done = b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Jump(head)
		b.Bind(head)
		b.Branch(b.CompareImmediate(b.Get(k), ir.BranchEqual, 0), done)
		b.Jump(body)
		b.Bind(body)
	}
	for s := g.r.Intn(6) + 1; s > 0; s-- {
		g.stmt(depth)
	}
	if loop {
		b.Set(k, b.Sub(b.Get(k), b.Const(1)))
		b.Jump(head)
		b.Bind(done)
	}
	export := g.r.Intn(2) == 0
	var r n.Value
	if export {
		r = b.Add(b.Get(g.anyInt()), b.Const(3))
	}
	g.ints = g.ints[:len(g.ints)-1]
	g.fps = g.fps[:len(g.fps)-1]
	if export {
		b.Leave(r)
		v := g.anyInt()
		b.Set(v, b.Xor(b.Get(v), r))
	} else {
		b.Leave()
	}
}

func zzScopeGen(seed int64) *n.Builder {
	r := rand.New(rand.NewSource(seed))
	b := n.NewBuilder()
	g := &zzSG{r: r, b: b, budget: 40 + r.Intn(200), calls: seed%3 == 0}
	g.x = b.Param()
	g.ptrV = b.Var(b.ParamNative())
	root := []n.Var{b.Var(g.x), b.Var(b.Add(g.x, b.Const(1)))}
	for i := r.Intn(6); i > 0; i-- {
		root = append(root, b.Var(b.Const(uint64(i*17))))
	}
	g.ints = [][]n.Var{root}
	g.fps = [][]n.Var{nil}
	if r.Intn(2) == 0 {
		g.fps[0] = []n.Var{b.Var(b.Float64(2.5))}
	}
	for g.budget > 0 {
		g.stmt(0)
	}
	acc := b.Get(root[0])
	for _, v := range root[1:] {
		acc = b.Xor(acc, b.Get(v))
	}
	b.Return(acc)
	return b
}
