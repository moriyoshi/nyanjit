//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import n "github.com/moriyoshi/nyanjit"

func prog2(tg *Targets) *n.Builder {
	b := n.NewBuilder()
	t1, t2, t3 := b.ParamBits(64), b.ParamBits(32), b.ParamNative()
	_, _, _ = t1, t2, t3
	v1 := b.Var(b.ConstBits(64, 0x0))
	_ = v1
	b.Enter()
	L1, L2, L3 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L1, b.ConstBits(64, 0))
	hL1 := b.BindParamsBits(L1, []uint8{64})
	b.BranchArgs(b.IsZero(hL1[0]), L3, nil, L2, hL1)
	qL1 := b.BindParamsBits(L2, []uint8{64})
	t16 := qL1[0]
	_ = t16
	b.Enter()
	L4, L5, L6 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L4, b.ConstBits(64, 0), b.ConstBits(32, 0x0))
	hL4 := b.BindParamsBits(L4, []uint8{64, 32})
	b.BranchArgs(b.IsZero(hL4[0]), L6, nil, L5, hL4)
	qL4 := b.BindParamsBits(L5, []uint8{64, 32})
	t20 := qL4[0]
	_ = t20
	t21 := qL4[1]
	_ = t21
	b.Enter()
	t22 := b.CallGo(b.ConstNative(tg.goFn[1]), b.ConstBits(64, 0x0))
	_ = t22
	kL4 := b.Sub(t20, b.ConstBits(64, 1))
	xL4 := b.ConstBits(32, 0x0)
	t23 := b.Get(v1)
	_ = t23
	b.Leave(kL4, xL4, t23)
	b.Set(v1, mix(b, b.Get(v1), t23))
	b.JumpArgs(L4, kL4, xL4)
	b.Bind(L6)
	kL1 := b.Sub(t16, b.ConstBits(64, 1))
	t26 := b.Get(v1)
	_ = t26
	b.Leave(kL1, t26)
	b.Set(v1, mix(b, b.Get(v1), t26))
	b.JumpArgs(L1, kL1)
	b.Bind(L3)
	t28 := b.Get(v1)
	_ = t28
	b.Leave(t28)
	b.Set(v1, mix(b, b.Get(v1), t28))
	tRoot := mix(b, b.Get(v1), b.Get(v1))
	_ = tRoot
	b.Return(tRoot)
	return b
}
