//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le || 386)

package varfuzz

import n "github.com/moriyoshi/nyanjit"

func prog185() *n.Builder {
	b := n.NewBuilder()
	t1, t2, t3 := b.ParamBits(64), b.ParamBits(32), b.ParamNative()
	_, _, _ = t1, t2, t3
	v1 := b.Var(b.ConstBits(64, 0x0))
	v2 := b.Var(b.ConstBits(8, 0x0))
	v3 := b.Var(b.ConstBits(8, 0x0))
	v4 := b.Var(b.ConstBits(8, 0x0))
	b.Enter()
	v5 := b.Var(b.ConstBits(8, 0x0))
	b.Set(v2, b.Select(b.Select(b.ConstBits(8, 0x0), b.ConstBits(8, 0x0), b.ConstBits(8, 0x0)), b.ConstBits(8, 0x0), b.ConstBits(8, 0x0)))
	t15 := mix(b, b.Get(v1), b.CastUnsigned(b.Get(v5), 64))
	b.Leave(t15)
	tRoot := mix(b, mix(b, mix(b, mix(b, b.Get(v1), b.Get(v1)), b.CastUnsigned(b.Get(v2), 64)), b.CastUnsigned(b.Get(v3), 64)), b.CastUnsigned(b.Get(v4), 64))
	b.Return(tRoot)
	return b
}
