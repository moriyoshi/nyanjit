//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package varfuzz

import (
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

func prog445(tg *Targets) *n.Builder {
	b := n.NewBuilder()
	t1, t2, t3 := b.ParamBits(64), b.ParamBits(32), b.ParamNative()
	_, _, _ = t1, t2, t3
	v1 := b.Var(b.ConstBits(64, 0x0))
	_ = v1
	v2 := b.Var(b.ConstBits(32, 0x0))
	_ = v2
	v3 := b.Var(b.ConstBits(32, 0x0))
	_ = v3
	v4 := b.Var(t3)
	_ = v4
	b.Enter()
	t23 := b.Get(v1)
	_ = t23
	b.Leave(t23)
	b.Set(v1, mix(b, b.Get(v1), t23))
	b.Enter()
	skL1 := b.Var(b.ConstBits(8, 0))  // loop state
	sxL1 := b.Var(b.ConstBits(64, 0)) // loop state
	L1, L2, L3 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Set(skL1, b.ConstBits(8, 1))
	b.Set(sxL1, b.ConstBits(64, 0x0))
	b.Jump(L1)
	b.Bind(L1)
	hL1 := []n.Value{b.Get(skL1), b.Get(sxL1)}
	b.Branch(b.IsZero(hL1[0]), L3)
	b.Jump(L2)
	b.Bind(L2)
	qL1 := hL1 // the head snapshots dominate the body
	t26 := qL1[0]
	_ = t26
	t27 := qL1[1]
	_ = t27
	b.Enter()
	skL4 := b.Var(b.ConstBits(16, 0)) // loop state
	sxL4 := b.Var(b.ConstBits(16, 0)) // loop state
	L4, L5, L6 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Set(skL4, b.ConstBits(16, 1))
	b.Set(sxL4, b.ConstBits(16, 0x0))
	b.Jump(L4)
	b.Bind(L4)
	hL4 := []n.Value{b.Get(skL4), b.Get(sxL4)}
	b.Branch(b.IsZero(hL4[0]), L6)
	b.Jump(L5)
	b.Bind(L5)
	qL4 := hL4 // the head snapshots dominate the body
	t30 := qL4[0]
	_ = t30
	t31 := qL4[1]
	_ = t31
	b.Enter()
	v10 := b.Var(b.ConstBits(32, 0x0))
	_ = v10
	L7, L8 := b.NewLabel(), b.NewLabel()
	L9 := b.NewLabel()
	b.Branch(b.Compare(b.ConstBits(32, 0x0), ir.BranchNotEqual, b.ConstBits(32, 0x0)), L7)
	b.Jump(L9)
	b.Bind(L7)
	b.Jump(L8)
	b.Bind(L9)
	b.Jump(L8)
	b.Bind(L8)
	t36 := b.CallC(b.ConstNative(tg.cbFn[3]), b.CastUnsigned(t31, builder.NativeBits), b.ConstNative(0), b.ConstNative(0))
	_ = t36
	kL4 := b.Sub(t30, b.ConstBits(16, 1))
	xL4 := b.Mul(b.ConstBits(16, 0x0), b.ConstBits(16, 0x0))
	t37 := mix(b, b.Get(v1), b.CastUnsigned(b.Get(v10), 64))
	_ = t37
	b.Leave(kL4, xL4, t37)
	b.Set(v1, mix(b, b.Get(v1), t37))
	b.Set(skL4, kL4)
	b.Set(sxL4, xL4)
	b.Jump(L4)
	b.Bind(L6)
	kL1 := b.Sub(t26, b.ConstBits(8, 1))
	xL1 := t23
	t40 := b.Get(v1)
	_ = t40
	b.Leave(kL1, xL1, t40)
	b.Set(v1, mix(b, b.Get(v1), t40))
	b.Set(skL1, kL1)
	b.Set(sxL1, xL1)
	b.Jump(L1)
	b.Bind(L3)
	t43 := b.Get(v1)
	_ = t43
	b.Leave(t43)
	b.Set(v1, mix(b, b.Get(v1), t43))
	tRoot := mix(b, mix(b, mix(b, mix(b, b.Get(v1), b.Get(v1)), b.CastUnsigned(b.Get(v2), 64)), b.CastUnsigned(b.Get(v3), 64)), b.CastUnsigned(b.Get(v4), 64))
	_ = tRoot
	b.Return(tRoot)
	return b
}
