package workload

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// The scope workloads keep their scope and Var counts proportional to n. They
// exercise mutable storage and nested lifetimes that value chains do not.

// ManyVars declares n Vars in one scope, updates each once and sums them.
func ManyVars(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	x := b.Param()
	m := max(n, 1)
	vars := make([]nyanjit.Var, m)
	b.Enter()
	for i := range vars {
		vars[i] = b.Var(b.Add(x, b.Const(uint64(i))))
	}
	for i := range vars {
		b.Set(vars[i], b.Xor(b.Get(vars[i]), b.Get(vars[(i+7)%m])))
	}
	r := b.Get(vars[0])
	for _, v := range vars[1:] {
		r = b.Add(r, b.Get(v))
	}
	b.Leave(r)
	b.Return(r)
	return finish(b)
}

// ManyVarsLoop updates n Vars inside a four-iteration loop.
func ManyVarsLoop(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	x := b.Param()
	m := max(n, 1)
	vars := make([]nyanjit.Var, m)
	for i := range vars {
		vars[i] = b.Var(b.Add(x, b.Const(uint64(i))))
	}
	k := b.Var(b.Const(4))
	head, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Jump(head)
	b.Bind(head)
	b.Branch(b.CompareImmediate(b.Get(k), ir.BranchEqual, 0), done)
	b.Jump(body)
	b.Bind(body)
	for i := range vars {
		b.Set(vars[i], b.Add(b.Get(vars[i]), b.Get(vars[(i+1)%m])))
	}
	b.Set(k, b.Sub(b.Get(k), b.Const(1)))
	b.Jump(head)
	b.Bind(done)
	r := b.Get(vars[0])
	for _, v := range vars[1:] {
		r = b.Xor(r, b.Get(v))
	}
	b.Return(r)
	return finish(b)
}

// DeepScopes nests n scopes, each declaring a Var.
func DeepScopes(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	v := b.Var(b.Param())
	for i := 0; i < n; i++ {
		b.Enter()
		k := b.Var(b.Const(uint64(i + 1)))
		b.Set(v, b.Add(b.Get(v), b.Get(k)))
	}
	for i := 0; i < n; i++ {
		b.Set(v, b.Xor(b.Get(v), b.Const(uint64(i+1))))
		b.Leave()
	}
	b.Return(b.Get(v))
	return finish(b)
}

// DeepScopeLoops nests n scopes, each holding a two-iteration loop.
func DeepScopeLoops(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	v := b.Var(b.Param())
	d := n
	for i := 0; i < d; i++ {
		b.Enter()
		k := b.Var(b.Const(2))
		head, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Jump(head)
		b.Bind(head)
		b.Branch(b.CompareImmediate(b.Get(k), ir.BranchEqual, 0), done)
		b.Jump(body)
		b.Bind(body)
		b.Set(v, b.Add(b.Get(v), b.Const(uint64(i))))
		b.Set(k, b.Sub(b.Get(k), b.Const(1)))
		b.Jump(head)
		b.Bind(done)
	}
	for i := 0; i < d; i++ {
		b.Leave()
	}
	b.Return(b.Get(v))
	return finish(b)
}

// NestedLoops nests n single-iteration loops, each in its own scope.
func NestedLoops(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	acc := b.Var(b.Param())
	d := n
	type lv struct {
		k                nyanjit.Var
		head, body, done nyanjit.Label
	}
	ls := make([]lv, d)
	for i := 0; i < d; i++ {
		b.Enter()
		l := lv{b.Var(b.Const(1)), b.NewLabel(), b.NewLabel(), b.NewLabel()}
		ls[i] = l
		b.Jump(l.head)
		b.Bind(l.head)
		b.Branch(b.CompareImmediate(b.Get(l.k), ir.BranchEqual, 0), l.done)
		b.Jump(l.body)
		b.Bind(l.body)
		b.Set(acc, b.Add(b.Get(acc), b.Const(uint64(i))))
	}
	for i := d - 1; i >= 0; i-- {
		l := ls[i]
		b.Set(l.k, b.Sub(b.Get(l.k), b.Const(1)))
		b.Jump(l.head)
		b.Bind(l.done)
		b.Leave()
	}
	b.Return(b.Get(acc))
	return finish(b)
}
