//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import n "github.com/moriyoshi/nyanjit"

func prog251() *n.Builder {
	b := n.NewBuilder()
	t1, t2, t3 := b.ParamBits(64), b.ParamBits(32), b.ParamNative()
	_, _, _ = t1, t2, t3
	v1 := b.Var(b.ConstBits(64, 0x1))
	_ = v1
	b.Enter()
	L1, L2, L3 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L1, b.ConstBits(32, 1), b.ConstBits(8, 0x0))
	hL1 := b.BindParamsBits(L1, []uint8{32, 8})
	b.BranchArgs(b.IsZero(hL1[0]), L3, nil, L2, hL1)
	qL1 := b.BindParamsBits(L2, []uint8{32, 8})
	t19 := qL1[0]
	_ = t19
	t20 := qL1[1]
	_ = t20
	b.Enter()
	L4, L5, L6 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L4, b.ConstBits(64, 0), t19)
	hL4 := b.BindParamsBits(L4, []uint8{64, 32})
	b.BranchArgs(b.IsZero(hL4[0]), L6, nil, L5, hL4)
	qL4 := b.BindParamsBits(L5, []uint8{64, 32})
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
	b.JumpArgs(L4, kL4, xL4)
	b.Bind(L6)
	kL1 := b.Sub(t19, b.ConstBits(32, 1))
	xL1 := b.ConstBits(8, 0x0)
	t29 := b.Get(v1)
	_ = t29
	b.Leave(kL1, xL1, t29)
	b.Set(v1, mix(b, b.Get(v1), t29))
	b.JumpArgs(L1, kL1, xL1)
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
