package irconform

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// vectorVarCases keep vectors and masks of each vector type in Vars across
// loops, branches and nested scopes. Each loads its vectors from the buffer
// and stores the final vector at offset 64.
func vectorVarCases() []Case {
	var cases []Case
	inputs := [][]uint64{{0}, {1}, {6}}
	for _, t := range vectorTypes {
		suffix := "-" + itoa(int64(t.Lanes)) + "x" + itoa(t.LaneBits)
		bits := t.LaneBits
		add := func(name string, build func(b *nyanjit.Builder)) {
			cases = append(cases, Case{Name: "control/vector-var-" + name + suffix, Buffer: vectorSize, Inputs: inputs, Build: built(build)})
		}
		// A vector and a mask Var carried around a loop.
		add("mask-loop", func(b *nyanjit.Builder) {
			buffer, n := b.ParamNative(), b.Param()
			x := b.VLoad(t, buffer)
			v := b.Var(x)
			m := b.Var(b.VCompare(x, ir.BranchAbove, b.VLoad(t, b.Add(buffer, b.ConstNative(32)))).Value())
			bits64 := b.Var(b.Const(0))
			i := b.Var(b.Const(0))
			loop, done := b.NewLabel(), b.NewLabel()
			b.Bind(loop)
			b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
			b.Set(bits64, b.Xor(b.RotateLeft(b.Get(bits64), 7), b.CastUnsigned(b.MaskBits(b.Get(m).Mask()), 64)))
			s := b.Splat(t, b.CastUnsigned(b.Add(b.Get(i), b.Const(0x35)), bits))
			b.Set(v, b.Add(b.RotateLeft(b.Get(v), 3), s))
			b.Set(m, b.VCompare(b.Get(v), ir.BranchLess, s).Value())
			b.Set(i, b.Add(b.Get(i), b.Const(1)))
			b.Jump(loop)
			b.Bind(done)
			b.VStore(b.Add(buffer, b.ConstNative(64)), b.Get(v))
			b.Return(b.Xor(b.Get(bits64), b.CastUnsigned(b.MaskBits(b.Get(m).Mask()), 64)))
		})
		// Vars set on both arms of a branch inside nested scopes.
		add("scopes", func(b *nyanjit.Builder) {
			buffer, n := b.ParamNative(), b.Param()
			y := b.VLoad(t, b.Add(buffer, b.ConstNative(32)))
			b.Enter()
			v := b.Var(b.VLoad(t, buffer))
			i := b.Var(b.Const(0))
			loop, done := b.NewLabel(), b.NewLabel()
			b.Bind(loop)
			b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
			b.Enter()
			w := b.Var(b.Xor(b.Get(v), y))
			odd, next := b.NewLabel(), b.NewLabel()
			b.Branch(b.IsZero(b.And(b.Get(i), b.Const(1))).Not(), odd)
			b.Set(w, b.Sub(b.Get(w), b.ShiftRight(y, 1)))
			b.Jump(next)
			b.Bind(odd)
			b.Set(w, b.Add(b.ShiftLeft(b.Get(w), 1), b.Get(v)))
			b.Jump(next)
			b.Bind(next)
			b.Set(v, b.Get(w))
			b.Leave()
			b.Set(i, b.Add(b.Get(i), b.Const(1)))
			b.Jump(loop)
			b.Bind(done)
			result := b.Get(v)
			b.Leave(result)
			b.VStore(b.Add(buffer, b.ConstNative(64)), result)
			b.Return(b.CastUnsigned(b.ExtractLane(result, t.Lanes-1), 64))
		})
	}
	return cases
}
