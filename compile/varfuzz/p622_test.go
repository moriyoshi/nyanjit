//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import n "github.com/moriyoshi/nyanjit"

func prog622() *n.Builder { return prog622v(false) }

func prog622v(probe bool) *n.Builder {
	b := n.NewBuilder()
	t1, t2, t3 := b.ParamBits(64), b.ParamBits(32), b.ParamNative()
	_, _, _ = t1, t2, t3
	v1 := b.Var(b.ConstBits(64, 0x0))
	_ = v1
	v2 := b.Var(b.ConstBits(16, 0x0))
	_ = v2
	v3 := b.Var(b.Float64(2))
	_ = v3
	v4 := b.Var(b.ConstBits(32, 0x0))
	_ = v4
	v5 := b.Var(b.ConstBits(32, 0x0))
	_ = v5
	v6 := b.Var(b.ConstBits(16, 0x0))
	_ = v6
	v7 := b.Var(b.ConstBits(64, 0x0))
	_ = v7
	b.Enter()
	v8 := b.Var(b.ConstBits(16, 0x0))
	_ = v8
	v9 := b.Var(b.ConstBits(32, 0x0))
	_ = v9
	v10 := b.Var(b.ConstBits(32, 0x0))
	_ = v10
	v11 := b.Var(b.Float64(1))
	_ = v11
	v12 := b.Var(b.ConstBits(64, 0x0))
	_ = v12
	v13 := b.Var(b.ConstBits(32, 0x0))
	_ = v13
	v14 := b.Var(b.ConstBits(8, 0x0))
	_ = v14
	v15 := b.Var(b.ConstBits(64, 0x0))
	_ = v15
	v16 := b.Var(b.ConstBits(8, 0x0))
	_ = v16
	v17 := b.Var(b.ConstBits(16, 0x0))
	_ = v17
	L1, L2, L3 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L1, b.ConstBits(16, 1), b.ConstBits(8, 0x0))
	hL1 := b.BindParamsBits(L1, []uint8{16, 8})
	b.BranchArgs(b.IsZero(hL1[0]), L3, nil, L2, hL1)
	qL1 := b.BindParamsBits(L2, []uint8{16, 8})
	t9 := qL1[0]
	_ = t9
	t10 := qL1[1]
	_ = t10
	b.Enter()
	v18 := b.Var(b.ConstBits(16, 0x0))
	_ = v18
	L4, L5, L6 := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.JumpArgs(L4, b.ConstBits(64, 0))
	hL4 := b.BindParamsBits(L4, []uint8{64})
	b.BranchArgs(b.IsZero(hL4[0]), L6, nil, L5, hL4)
	qL4 := b.BindParamsBits(L5, []uint8{64})
	t12 := qL4[0]
	_ = t12
	b.Enter()
	b.Set(v8, b.CastUnsigned(t10, 16))
	b.Set(v15, t3)
	kL4 := b.Sub(t12, b.ConstBits(64, 1))
	t13 := b.Get(v1)
	_ = t13
	b.Leave(kL4, t13)
	b.Set(v1, mix(b, b.Get(v1), t13))
	b.JumpArgs(L4, kL4)
	b.Bind(L6)
	kL1 := b.Sub(t9, b.ConstBits(16, 1))
	xL1 := b.Xor(b.CastUnsigned(t9, 8), b.CastUnsigned(b.Get(v1), 8))
	t24 := mix(b, b.Get(v1), b.CastUnsigned(b.Get(v18), 64))
	_ = t24
	b.Leave(kL1, xL1, t24)
	b.Set(v1, mix(b, b.Get(v1), t24))
	b.JumpArgs(L1, kL1, xL1)
	b.Bind(L3)
	if probe {
		// return individual var values packed: v8 (16) | v15 low 16 << 16 | v12 low16 <<32 | v9 low 16 << 48
		r := b.Or(b.CastUnsigned(b.Get(v8), 64), b.ShiftLeft(b.And(b.Get(v15), b.Const(0xffff)), 16))
		r = b.Or(r, b.ShiftLeft(b.And(b.Get(v12), b.Const(0xffff)), 32))
		r = b.Or(r, b.ShiftLeft(b.CastUnsigned(b.CastUnsigned(b.Get(v9), 16), 64), 48))
		b.Leave(r)
		b.Return(r)
		return b
	}
	t27 := mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, b.Get(v1), b.CastUnsigned(b.Get(v8), 64)), b.CastUnsigned(b.Get(v9), 64)), b.CastUnsigned(b.Get(v10), 64)), b.Get(v11)), b.Get(v12)), b.CastUnsigned(b.Get(v13), 64)), b.CastUnsigned(b.Get(v14), 64)), b.Get(v15)), b.CastUnsigned(b.Get(v16), 64)), b.CastUnsigned(b.Get(v17), 64))
	_ = t27
	b.Leave(t27)
	b.Set(v1, mix(b, b.Get(v1), t27))
	tRoot := mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, b.Get(v1), b.Get(v1)), b.CastUnsigned(b.Get(v2), 64)), b.Get(v3)), b.CastUnsigned(b.Get(v4), 64)), b.CastUnsigned(b.Get(v5), 64)), b.CastUnsigned(b.Get(v6), 64)), b.Get(v7))
	_ = tRoot
	b.Return(tRoot)
	return b
}
