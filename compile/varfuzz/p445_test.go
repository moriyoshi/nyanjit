//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import n "github.com/moriyoshi/nyanjit"

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
	L1, L2, L3 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L1, b.ConstBits(8, 1), b.ConstBits(64, 0x0))
	hL1 := b.BindParamsBits(L1, []uint8{8, 64})
	b.BranchArgs(b.IsZero(hL1[0]), L3, nil, L2, hL1)
	qL1 := b.BindParamsBits(L2, []uint8{8, 64})
	t26 := qL1[0]
	_ = t26
	t27 := qL1[1]
	_ = t27
	b.Enter()
	L4, L5, L6 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L4, b.ConstBits(16, 1), b.ConstBits(16, 0x0))
	hL4 := b.BindParamsBits(L4, []uint8{16, 16})
	b.BranchArgs(b.IsZero(hL4[0]), L6, nil, L5, hL4)
	qL4 := b.BindParamsBits(L5, []uint8{16, 16})
	t30 := qL4[0]
	_ = t30
	t31 := qL4[1]
	_ = t31
	b.Enter()
	v10 := b.Var(b.ConstBits(32, 0x0))
	_ = v10
	L7, L8 := b.NewLabel(), b.NewLabel()
	L9 := b.NewLabel()
	b.Branch(b.Compare(b.ConstBits(32, 0x0), n.BranchNotEqual, b.ConstBits(32, 0x0)), L7, L9)
	b.Bind(L7)
	b.Jump(L8)
	b.Bind(L9)
	b.Jump(L8)
	b.Bind(L8)
	t36 := b.CallC(b.ConstNative(tg.cbFn[3]), b.CastUnsigned(t31, n.NativeBits), b.ConstNative(0), b.ConstNative(0))
	_ = t36
	kL4 := b.Sub(t30, b.ConstBits(16, 1))
	xL4 := b.Mul(b.ConstBits(16, 0x0), b.ConstBits(16, 0x0))
	t37 := mix(b, b.Get(v1), b.CastUnsigned(b.Get(v10), 64))
	_ = t37
	b.Leave(kL4, xL4, t37)
	b.Set(v1, mix(b, b.Get(v1), t37))
	b.JumpArgs(L4, kL4, xL4)
	b.Bind(L6)
	kL1 := b.Sub(t26, b.ConstBits(8, 1))
	xL1 := t23
	t40 := b.Get(v1)
	_ = t40
	b.Leave(kL1, xL1, t40)
	b.Set(v1, mix(b, b.Get(v1), t40))
	b.JumpArgs(L1, kL1, xL1)
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
