//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import n "github.com/moriyoshi/nyanjit"

func prog24() *n.Builder {
	b := n.NewBuilder()
	t1, t2, t3 := b.ParamBits(64), b.ParamBits(32), b.ParamNative()
	_, _, _ = t1, t2, t3
	v1 := b.Var(b.ConstBits(64, 0x0))
	L1, L2, L3 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L1, b.ConstBits(8, 1), b.ConstBits(32, 0x0))
	hL1 := b.BindParamsBits(L1, []uint8{8, 32})
	b.BranchArgs(b.IsZero(hL1[0]), L3, nil, L2, hL1)
	qL1 := b.BindParamsBits(L2, []uint8{8, 32})
	t6 := qL1[0]
	t7 := qL1[1]
	_ = t7
	b.Enter()
	v4 := b.Var(b.ConstBits(8, 0x0))
	b.Enter()
	b.Enter()
	b.Set(v4, b.ConstBits(8, 0x0))
	t20 := b.Get(v1)
	b.Leave(t20)
	b.Set(v1, mix(b, b.Get(v1), t20))
	t21 := b.Get(v1)
	b.Leave(t21)
	kL1 := b.Sub(t6, b.ConstBits(8, 1))
	xL1 := b.ConstBits(32, 0x0)
	t22 := mix(b, b.Get(v1), b.CastUnsigned(b.Get(v4), 64))
	b.Leave(kL1, xL1, t22)
	b.Set(v1, mix(b, b.Get(v1), t22))
	b.JumpArgs(L1, kL1, xL1)
	b.Bind(L3)
	tRoot := mix(b, b.Get(v1), b.Get(v1))
	b.Return(tRoot)
	return b
}
