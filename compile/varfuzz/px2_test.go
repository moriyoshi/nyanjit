//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import n "github.com/moriyoshi/nyanjit"

func progX2() *n.Builder {
	b := n.NewBuilder()
	t1, t2, t3 := b.ParamBits(64), b.ParamBits(32), b.ParamNative()
	_, _, _ = t1, t2, t3
	v1 := b.Var(b.ConstBits(64, 0x0))
	_ = v1
	v2 := b.Var(b.ConstBits(32, 0x0))
	_ = v2
	v3 := b.Var(b.ConstBits(16, 0x0))
	_ = v3
	b.Enter()
	v4 := b.Var(b.ConstBits(8, 0x0))
	_ = v4
	v5 := b.Var(b.ConstBits(16, 0x0))
	_ = v5
	v6 := b.Var(b.ConstBits(16, 0x0))
	_ = v6
	v7 := b.Var(b.ConstBits(16, 0x0))
	_ = v7
	v8 := b.Var(b.ConstBits(32, 0x0))
	_ = v8
	v9 := b.Var(b.ConstBits(16, 0x0))
	_ = v9
	v10 := b.Var(b.ConstBits(32, 0x0))
	_ = v10
	v11 := b.Var(b.ConstBits(8, 0x0))
	_ = v11
	v17 := b.Var(b.ConstBits(64, 0x0))
	_ = v17
	t15 := mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, mix(b, b.Get(v1), b.CastUnsigned(b.Get(v4), 64)), b.CastUnsigned(b.Get(v5), 64)), b.CastUnsigned(b.Get(v6), 64)), b.CastUnsigned(b.Get(v7), 64)), b.CastUnsigned(b.Get(v8), 64)), b.CastUnsigned(b.Get(v9), 64)), b.CastUnsigned(b.Get(v10), 64)), b.CastUnsigned(b.Get(v11), 64)), b.Get(v17))
	_ = t15
	b.Leave(t15)
	b.Set(v1, mix(b, b.Get(v1), t15))
	tRoot := mix(b, mix(b, mix(b, b.Get(v1), b.Get(v1)), b.CastUnsigned(b.Get(v2), 64)), b.CastUnsigned(b.Get(v3), 64))
	_ = tRoot
	b.Return(tRoot)
	return b
}
