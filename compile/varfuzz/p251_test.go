//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64 || 386)

package varfuzz

import n "github.com/moriyoshi/nyanjit"

func prog251() *n.Builder {
	b := n.NewBuilder()
	t1, t2, t3 := b.ParamBits(64), b.ParamBits(32), b.ParamNative()
	_, _, _ = t1, t2, t3
	v1 := b.Var(b.ConstBits(64, 0x1))
	_ = v1
	b.Enter()
	skL1 := b.Var(b.ConstBits(32, 0)) // loop state
	sxL1 := b.Var(b.ConstBits(8, 0))  // loop state
	L1, L2, L3 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Set(skL1, b.ConstBits(32, 1))
	b.Set(sxL1, b.ConstBits(8, 0x0))
	b.Jump(L1)
	b.Bind(L1)
	hL1 := []n.Value{b.Get(skL1), b.Get(sxL1)}
	b.Branch(b.IsZero(hL1[0]), L3)
	b.Jump(L2)
	b.Bind(L2)
	qL1 := hL1 // the head snapshots dominate the body
	t19 := qL1[0]
	_ = t19
	t20 := qL1[1]
	_ = t20
	b.Enter()
	skL4 := b.Var(b.ConstBits(64, 0)) // loop state
	sxL4 := b.Var(b.ConstBits(32, 0)) // loop state
	L4, L5, L6 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Set(skL4, b.ConstBits(64, 0))
	b.Set(sxL4, t19)
	b.Jump(L4)
	b.Bind(L4)
	hL4 := []n.Value{b.Get(skL4), b.Get(sxL4)}
	b.Branch(b.IsZero(hL4[0]), L6)
	b.Jump(L5)
	b.Bind(L5)
	qL4 := hL4 // the head snapshots dominate the body
	t23 := qL4[0]
	_ = t23
	t24 := qL4[1]
	_ = t24
	b.Enter()
	kL4 := b.Sub(t23, b.ConstBits(64, 1))
	xL4 := b.ConstBits(32, 0x0)
	t25 := b.Get(v1)
	_ = t25
	b.Leave(kL4, xL4, t25)
	b.Set(v1, mix(b, b.Get(v1), t25))
	b.Set(skL4, kL4)
	b.Set(sxL4, xL4)
	b.Jump(L4)
	b.Bind(L6)
	kL1 := b.Sub(t19, b.ConstBits(32, 1))
	xL1 := b.ConstBits(8, 0x0)
	t29 := b.Get(v1)
	_ = t29
	b.Leave(kL1, xL1, t29)
	b.Set(v1, mix(b, b.Get(v1), t29))
	b.Set(skL1, kL1)
	b.Set(sxL1, xL1)
	b.Jump(L1)
	b.Bind(L3)
	t32 := b.Get(v1)
	_ = t32
	b.Leave(t32)
	b.Set(v1, mix(b, b.Get(v1), t32))
	tRoot := mix(b, b.Get(v1), b.Get(v1))
	_ = tRoot
	b.Return(tRoot)
	return b
}
