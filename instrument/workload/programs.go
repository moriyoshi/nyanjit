package workload

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// Chain applies n integer operations to eight rotating accumulators, so
// register pressure stays low.
func Chain(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	acc := make([]nyanjit.Value, 8)
	for i := range acc {
		acc[i] = b.Add(x, b.Const(uint64(i+1)))
	}
	for i := 0; i < n; i++ {
		j := i % 8
		switch i % 3 {
		case 0:
			acc[j] = b.Add(acc[j], y)
		case 1:
			acc[j] = b.Xor(acc[j], acc[(j+1)%8])
		default:
			acc[j] = b.RotateLeft(acc[j], 7)
		}
	}
	s := acc[0]
	for _, a := range acc[1:] {
		s = b.Add(s, a)
	}
	b.Return(s)
	return finish(b)
}

// Pressure keeps n/2 values live until the end, forcing spills.
func Pressure(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	vals := []nyanjit.Value{}
	v := x
	for i := 0; i < n/2; i++ {
		v = b.Add(b.Mul(v, y), b.Const(uint64(i)))
		vals = append(vals, v)
	}
	s := vals[0]
	for _, a := range vals[1:] {
		s = b.Xor(s, a)
	}
	b.Return(s)
	return finish(b)
}

// FP applies n float64 operations to eight accumulators.
func FP(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	x := b.Param()
	f := b.Float64(1.5)
	_ = x
	acc := make([]nyanjit.Value, 8)
	for i := range acc {
		acc[i] = b.FAdd(f, b.Float64(float64(i)))
	}
	for i := 0; i < n; i++ {
		j := i % 8
		if i%2 == 0 {
			acc[j] = b.FMul(acc[j], acc[(j+3)%8])
		} else {
			acc[j] = b.FAdd(acc[j], b.Float64(0.25))
		}
	}
	s := acc[0]
	for _, a := range acc[1:] {
		s = b.FAdd(s, a)
	}
	b.Return(s)
	return finish(b)
}

// Loops runs n/10 sequential loops, each in its own scope with a Var counter.
func Loops(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	acc := b.Var(x)
	yv := b.Var(y)
	for i := 0; i < n/10; i++ {
		b.Enter()
		k := b.Var(b.Const(3))
		head, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Jump(head)
		b.Bind(head)
		b.Branch(b.CompareImmediate(b.Get(k), ir.BranchEqual, 0), done)
		b.Jump(body)
		b.Bind(body)
		b.Set(acc, b.Xor(b.Add(b.Get(acc), b.Get(yv)), b.Const(uint64(i))))
		b.Set(k, b.Sub(b.Get(k), b.Const(1)))
		b.Jump(head)
		b.Bind(done)
		b.Leave()
	}
	b.Return(b.Get(acc))
	return finish(b)
}

// Calls makes n outbound C calls to a fixed address, with a float64 value
// live across each call. Compile it as a foreign C entry; never run it.
func Calls(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	x := b.Param()
	f := b.FAdd(x, b.Float64(1.5))
	for i := 0; i < n; i++ {
		f = b.FAdd(f, b.Float64(0.25))
		x = b.Xor(x, b.CallC(b.ConstNative(0x1234), x))
	}
	b.Return(b.Xor(x, f))
	return finish(b)
}

// Vectors applies n rotate, add and xor rounds to a 4×32-bit vector loaded
// from and stored back to the first argument.
func Vectors(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	ptr := b.ParamNative()
	typ := ir.Vec(4, 32)
	seed := b.VLoad(typ, ptr)
	v := seed
	for i := 0; i < n; i++ {
		v = b.Xor(b.RotateRight(v, 7), b.Add(v, seed))
	}
	b.VStore(ptr, v)
	b.Return(b.ConstNative(0))
	return finish(b)
}

// Scopes nests n scopes that each update one outer Var on entry and exit.
func Scopes(n int) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	v := b.Var(b.Param())
	for i := 0; i < n; i++ {
		b.Enter()
		b.Set(v, b.Add(b.Get(v), b.Const(uint64(i+1))))
	}
	for i := 0; i < n; i++ {
		b.Set(v, b.Xor(b.Get(v), b.Const(uint64(i+1))))
		b.Leave()
	}
	b.Return(b.Get(v))
	return finish(b)
}

// OuterValues defines n values in the entry block and reads all of them in
// a loop body, using each value directly in the blocks its definition
// dominates. OuterValuesThreaded is the same function with the values
// threaded through the loop in Vars; the pair compares the two forms.
func OuterValues(n int) *ir.SealedProgram { return outerValues(n, false) }

// OuterValuesThreaded is OuterValues with the outer values threaded through
// the loop in Vars by the front end.
func OuterValuesThreaded(n int) *ir.SealedProgram { return outerValues(n, true) }

func outerValues(n int, threaded bool) *ir.SealedProgram {
	b := nyanjit.NewBuilder()
	x, y := b.Param(), b.Param()
	outer := make([]nyanjit.Value, n)
	for k := range outer {
		outer[k] = b.Add(b.Mul(x, b.Const(uint64(k+1))), y)
	}
	accv, iv := b.Var(b.Const(0)), b.Var(b.Const(16))
	var threads []nyanjit.Var
	if threaded {
		threads = make([]nyanjit.Var, n)
		for k, v := range outer {
			threads[k] = b.Var(v)
		}
	}
	loop, done := b.NewLabel(), b.NewLabel()
	b.Jump(loop)
	b.Bind(loop)
	acc, i := b.Get(accv), b.Get(iv)
	values := outer
	if threaded {
		values = make([]nyanjit.Value, n)
		for k, v := range threads {
			values[k] = b.Get(v)
		}
	}
	for k, v := range values {
		if k%2 == 0 {
			acc = b.Add(acc, v)
		} else {
			acc = b.Xor(acc, v)
		}
	}
	next := b.Sub(i, b.Const(1))
	b.Set(accv, acc)
	b.Set(iv, next)
	b.Branch(b.IsZero(next), done)
	b.Jump(loop)
	b.Bind(done)
	b.Return(b.Get(accv))
	return finish(b)
}
