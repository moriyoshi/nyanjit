package workload

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// Tiny adds its two arguments. It has a fixed size and ignores n.
func Tiny(_ int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	b.Return(b.Add(b.ParamNative(), b.ParamNative()))
	return finish(b)
}

// Small is a loop of about 40 instructions, the size of a typical hot
// method. It has a fixed size and ignores n.
func Small(_ int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	acc := b.Var(x)
	k := b.Var(y)
	head, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Jump(head)
	b.Bind(head)
	b.Branch(b.CompareImmediate(b.Get(k), ir.BranchEqual, 0), done)
	b.Jump(body)
	b.Bind(body)
	v := b.Get(acc)
	for i := 0; i < 8; i++ {
		v = b.Xor(b.Add(v, b.Get(k)), b.Const(uint64(i*977+1)))
	}
	b.Set(acc, v)
	b.Set(k, b.Sub(b.Get(k), b.Const(1)))
	b.Jump(head)
	b.Bind(done)
	b.Return(b.Get(acc))
	return finish(b)
}
