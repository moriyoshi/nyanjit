//go:build linux && arm64

package varfuzz

import (
	"math/rand"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// Scope/loop-heavy generator with mixed-width entry parameters (8/16/32/64/
// native), 32-bit and vector Vars, calls inside scopes, and exports. Targets
// cached entry parameter-width facts and per-scope reservation masks.
// (reviewer-owned, identical in pre and post)

const (
	zpI64 = iota
	zpI32
	zpV4x32
	zpV2x64
	zpF64
	zpKinds
)

type zzPS struct {
	r      *rand.Rand
	b      *n.Builder
	ptr    n.Var
	vars   [][zpKinds][]n.Var // per scope level
	budget int
	calls  bool
	vecs   bool
	fp     bool
}

func (g *zzPS) any(kind int) (n.Var, bool) {
	lvl := len(g.vars) - 1
	for lvl >= 0 {
		if l := g.vars[lvl][kind]; len(l) > 0 && g.r.Intn(3) != 0 {
			return l[g.r.Intn(len(l))], true
		}
		lvl--
	}
	for lvl = len(g.vars) - 1; lvl >= 0; lvl-- {
		if l := g.vars[lvl][kind]; len(l) > 0 {
			return l[g.r.Intn(len(l))], true
		}
	}
	return n.Var{}, false
}

func (g *zzPS) i64() n.Value { v, _ := g.any(zpI64); return g.b.Get(v) }

func (g *zzPS) i32() n.Value {
	if v, ok := g.any(zpI32); ok {
		return g.b.Get(v)
	}
	return g.b.CastUnsigned(g.i64(), 32)
}

func (g *zzPS) vecType(kind int) ir.Type {
	if kind == zpV4x32 {
		return ir.Vec(4, 32)
	}
	return ir.Vec(2, 64)
}

func (g *zzPS) vec(kind int) n.Value {
	if v, ok := g.any(kind); ok {
		return g.b.Get(v)
	}
	return g.b.VLoad(g.vecType(kind), g.b.Get(g.ptr))
}

func (g *zzPS) expr(kind int) n.Value {
	b, r := g.b, g.r
	switch kind {
	case zpI64:
		a := g.i64()
		switch r.Intn(8) {
		case 0:
			return b.Add(a, b.Const(uint64(r.Intn(100))))
		case 1:
			return b.Xor(a, g.i64())
		case 2:
			return b.Sub(a, b.CastUnsigned(g.i32(), 64))
		case 3:
			return b.Mul(a, b.Const(uint64(r.Intn(9)+1)))
		case 4:
			if g.calls {
				if r.Intn(2) == 0 {
					return b.Add(a, b.CallC(b.ConstNative(0x61000), g.i64(), a))
				}
				return b.Xor(a, b.CallGo(b.ConstNative(0x62000), a, g.i64(), g.i64()))
			}
			return b.And(a, g.i64())
		case 5:
			return b.RotateLeft(a, uint8(r.Intn(63)+1))
		case 6:
			if g.vecs {
				return b.Add(a, b.CastUnsigned(b.ExtractLane(g.vec(zpV2x64), uint16(r.Intn(2))), 64))
			}
			return b.Or(a, g.i64())
		default:
			return b.Add(a, g.i64())
		}
	case zpI32:
		a := g.i32()
		switch r.Intn(5) {
		case 0:
			return b.Add(a, g.i32())
		case 1:
			return b.Mul(a, b.ConstBits(32, uint64(r.Uint32())))
		case 2:
			return b.RotateLeft(a, uint8(r.Intn(31)+1))
		case 3:
			return b.CastUnsigned(g.i64(), 32)
		default:
			return b.Xor(a, g.i32())
		}
	case zpV4x32, zpV2x64:
		a := g.vec(kind)
		t := g.vecType(kind)
		switch r.Intn(5) {
		case 0:
			return b.Add(a, g.vec(kind))
		case 1:
			return b.Xor(a, b.Splat(t, b.CastUnsigned(g.i64(), t.LaneBits)))
		case 2:
			return b.RotateRight(a, uint8(r.Intn(int(t.LaneBits)-1)+1))
		case 3:
			return b.InsertLane(a, uint16(r.Intn(int(t.Lanes))), b.CastUnsigned(g.i64(), t.LaneBits))
		default:
			return b.Sub(a, g.vec(kind))
		}
	default:
		v, ok := g.any(zpF64)
		if !ok {
			return b.Float64(1.5)
		}
		return b.FAdd(b.Get(v), b.Float64(float64(r.Intn(10))+0.25))
	}
}

func (g *zzPS) kind() int {
	for {
		k := []int{zpI64, zpI64, zpI64, zpI32, zpI32, zpV4x32, zpV2x64, zpF64}[g.r.Intn(8)]
		if (k == zpV4x32 || k == zpV2x64) && !g.vecs || k == zpF64 && !g.fp {
			continue
		}
		if _, ok := g.any(k); !ok && k != zpI64 {
			continue
		}
		return k
	}
}

func (g *zzPS) assign() {
	k := g.kind()
	v, _ := g.any(k)
	g.b.Set(v, g.expr(k))
}

func (g *zzPS) stmt(depth int) {
	b := g.b
	g.budget--
	switch k := g.r.Intn(14); {
	case k < 5 || g.budget <= 0:
		g.assign()
	case k < 6:
		switch g.r.Intn(3) {
		case 0:
			b.Store32(b.Get(g.ptr), g.i32())
		case 1:
			if g.vecs {
				kk := []int{zpV4x32, zpV2x64}[g.r.Intn(2)]
				b.VStore(b.Get(g.ptr), g.vec(kk))
				break
			}
			fallthrough
		default:
			b.Store64(b.Get(g.ptr), g.i64())
		}
	case k < 8 && depth < 8:
		g.scope(depth+1, false)
	case k < 11 && depth < 8:
		g.scope(depth+1, true)
	case k < 12:
		t, e, j := b.NewLabel(), b.NewLabel(), b.NewLabel()
		if g.r.Intn(2) == 0 {
			b.Branch(b.Compare(g.i32(), ir.BranchBelow, g.i32()), t)
			b.Jump(e)
		} else {
			b.Branch(b.Compare(g.i64(), ir.BranchEqual, g.i64()), t)
			b.Jump(e)
		}
		b.Bind(t)
		g.assign()
		b.Jump(j)
		b.Bind(e)
		g.assign()
		b.Jump(j)
		b.Bind(j)
	default:
		g.assign()
		g.assign()
	}
}

func (g *zzPS) scope(depth int, loop bool) {
	b := g.b
	b.Enter()
	g.vars = append(g.vars, [zpKinds][]n.Var{})
	lvl := len(g.vars) - 1
	nv := g.r.Intn(5)
	if g.r.Intn(8) == 0 {
		nv = 8 + g.r.Intn(30)
	}
	for i := 0; i < nv; i++ {
		k := g.kind()
		var init n.Value
		if k == zpI64 {
			init = b.Add(g.i64(), b.Const(uint64(i)))
		} else {
			init = g.expr(k)
		}
		g.vars[lvl][k] = append(g.vars[lvl][k], b.Var(init))
	}
	var c n.Var
	var head, body, done n.Label
	c32 := g.r.Intn(2) == 0
	if loop {
		if c32 {
			c = b.Var(b.ConstBits(32, uint64(g.r.Intn(3)+1)))
		} else {
			c = b.Var(b.Const(uint64(g.r.Intn(3) + 1)))
		}
		head, body, done = b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Jump(head)
		b.Bind(head)
		b.Branch(b.CompareImmediate(b.Get(c), ir.BranchEqual, 0), done)
		b.Jump(body)
		b.Bind(body)
	}
	for s := g.r.Intn(6) + 1; s > 0; s-- {
		g.stmt(depth)
	}
	if loop {
		cur := b.Get(c)
		one := b.Const(1)
		if c32 {
			one = b.ConstBits(32, 1)
		}
		b.Set(c, b.Sub(cur, one))
		b.Jump(head)
		b.Bind(done)
	}
	var exports []n.Value
	var ek []int
	if g.r.Intn(2) == 0 {
		for i := g.r.Intn(3) + 1; i > 0; i-- {
			k := g.kind()
			exports = append(exports, g.expr(k))
			ek = append(ek, k)
		}
	}
	g.vars = g.vars[:lvl]
	b.Leave(exports...)
	for i, e := range exports {
		if v, ok := g.any(ek[i]); ok {
			switch ek[i] {
			case zpF64:
				b.Set(v, b.FAdd(b.Get(v), e))
			default:
				b.Set(v, b.Xor(b.Get(v), e))
			}
		}
	}
}

func zzPScopeGen(seed int64) *n.Builder {
	r := rand.New(rand.NewSource(seed*31 + 7))
	b := n.NewBuilder()
	g := &zzPS{r: r, b: b, budget: 30 + r.Intn(160), calls: seed%3 == 0, vecs: seed%4 != 0, fp: seed%5 < 2}
	var root [zpKinds][]n.Var
	np := 1 + r.Intn(6)
	if seed%3 == 2 {
		np = 1 + r.Intn(10)
	}
	ptrDone := false
	for i := 0; i < np; i++ {
		switch r.Intn(6) {
		case 0, 1:
			root[zpI64] = append(root[zpI64], b.Var(b.Param()))
		case 2:
			root[zpI32] = append(root[zpI32], b.Var(b.ParamBits(32)))
		case 3:
			v := b.ParamBits([]uint8{8, 16}[r.Intn(2)])
			root[zpI32] = append(root[zpI32], b.Var(b.CastUnsigned(v, 32)))
		case 4:
			if !ptrDone {
				g.ptr = b.Var(b.ParamNative())
				ptrDone = true
			} else {
				root[zpI64] = append(root[zpI64], b.Var(b.CastUnsigned(b.ParamNative(), 64)))
			}
		default:
			root[zpI32] = append(root[zpI32], b.Var(b.ParamBits(32)))
		}
	}
	if !ptrDone {
		g.ptr = b.Var(b.ParamNative())
	}
	if len(root[zpI64]) == 0 {
		root[zpI64] = append(root[zpI64], b.Var(b.Const(5)))
	}
	g.vars = [][zpKinds][]n.Var{root}
	if g.vecs {
		g.vars[0][zpV4x32] = append(g.vars[0][zpV4x32], b.Var(b.VLoad(ir.Vec(4, 32), b.Get(g.ptr))))
		if r.Intn(2) == 0 {
			g.vars[0][zpV2x64] = append(g.vars[0][zpV2x64], b.Var(b.Splat(ir.Vec(2, 64), g.i64())))
		}
	}
	if g.fp {
		g.vars[0][zpF64] = append(g.vars[0][zpF64], b.Var(b.Float64(2.5)))
	}
	for g.budget > 0 {
		g.stmt(0)
	}
	acc := g.i64()
	for _, v := range g.vars[0][zpI64] {
		acc = b.Xor(acc, b.Get(v))
	}
	for _, v := range g.vars[0][zpI32] {
		acc = b.Add(acc, b.CastUnsigned(b.Get(v), 64))
	}
	for _, v := range g.vars[0][zpV4x32] {
		b.VStore(b.Get(g.ptr), b.Get(v))
	}
	for _, v := range g.vars[0][zpV2x64] {
		b.VStore(b.Get(g.ptr), b.Get(v))
	}
	b.Return(acc)
	return b
}
