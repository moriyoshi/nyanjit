package irconform

import (
	"math"
	"strconv"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

func built(build func(b *nyanjit.Builder)) func() (*ir.SealedProgram, error) {
	return func() (*ir.SealedProgram, error) {
		b := nyanjit.NewBuilder()
		build(b)
		return b.Finish()
	}
}

// boolValue merges 0 or 1 through a Var. It declares the Var, so it must
// run in the scope's entry sequence.
func boolValue(b *nyanjit.Builder, p nyanjit.Predicate) nyanjit.Value {
	result := b.Var(b.Const(0))
	yes, done := b.NewLabel(), b.NewLabel()
	b.Branch(p, yes)
	b.Jump(done)
	b.Bind(yes)
	b.Set(result, b.Const(1))
	b.Jump(done)
	b.Bind(done)
	return b.Get(result)
}

func controlCases() []Case {
	var cases []Case
	add := func(name string, inputs [][]uint64, build func(b *nyanjit.Builder)) {
		cases = append(cases, Case{Name: "control/" + name, Inputs: inputs, Build: built(build)})
	}
	for condition := ir.BranchEqual; condition <= ir.BranchAbove; condition++ {
		add("compare-"+conditionName[condition], binary(64), func(b *nyanjit.Builder) {
			x, y := b.Param(), b.Param()
			b.Return(boolValue(b, b.Compare(x, condition, y)))
		})
		for _, w := range []uint8{8, 16, 32} {
			add("compare-"+conditionName[condition]+"-"+itoa(w), binary(w), func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				b.Return(boolValue(b, b.Compare(x, condition, y)))
			})
		}
		for _, imm := range []int64{-1, 0, 5, math.MinInt32, 4096, 1 << 40} {
			add("compare-immediate-"+conditionName[condition]+"-"+itoa(imm), unary(64), func(b *nyanjit.Builder) {
				b.Return(boolValue(b, b.CompareImmediate(b.Param(), condition, imm)))
			})
		}
	}
	for _, w := range []uint8{32, 64} {
		var pairs [][]uint64
		for _, x := range floatValues(w) {
			for _, y := range floatValues(w) {
				pairs = append(pairs, []uint64{x, y})
			}
		}
		for c := ir.FloatEqual; c <= ir.FloatUnorderedEqual; c++ {
			add("fcompare-"+itoa(uint8(c))+"-"+itoa(w), pairs, func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				b.Return(boolValue(b, b.FComparePredicate(x, c, y)))
			})
		}
		// Float and integer comparisons combined in one predicate.
		add("fcompare-tree-"+itoa(w), pairs, func(b *nyanjit.Builder) {
			x, y := b.ParamBits(w), b.ParamBits(w)
			p := b.FComparePredicate(x, ir.FloatLess, y).Or(b.FComparePredicate(x, ir.FloatUnordered, x).Not().
				And(b.CompareImmediate(y, ir.BranchBelow, 1<<20)))
			b.Return(boolValue(b, p))
		})
	}
	add("carry", binary(64), func(b *nyanjit.Builder) {
		sum := b.Add(b.Param(), b.Param())
		b.Return(b.Add(boolValue(b, b.Carry(sum)), sum))
	})
	add("predicate-tree", ternary(64), func(b *nyanjit.Builder) {
		x, y, z := b.Param(), b.Param(), b.Param()
		p := b.Compare(x, ir.BranchLess, y).And(b.CompareImmediate(z, ir.BranchNotEqual, 0)).
			Or(b.Compare(y, ir.BranchBelow, x).Not().And(b.IsNegative(z))).
			Or(b.IsZero(x).And(b.IsZero(y).Not()))
		b.Return(boolValue(b, p))
	})
	add("max", binary(64), func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		result := b.Var(b.Const(0))
		pickX, pickY, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Branch(b.Compare(x, ir.BranchGreater, y), pickX)
		b.Jump(pickY)
		b.Bind(pickX)
		b.Set(result, x)
		b.Jump(done)
		b.Bind(pickY)
		b.Set(result, y)
		b.Jump(done)
		b.Bind(done)
		b.Return(b.Get(result))
	})
	add("var-loop", [][]uint64{{0}, {1}, {10}, {1000}}, func(b *nyanjit.Builder) {
		n := b.Param()
		acc, i := b.Var(b.Const(0)), b.Var(n)
		loop, done := b.NewLabel(), b.NewLabel()
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(i)), done)
		b.Set(acc, b.Add(b.Mul(b.Get(acc), b.Const(3)), b.Get(i)))
		b.Set(i, b.Sub(b.Get(i), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(acc))
	})
	add("ssa-loop", [][]uint64{{0, 5}, {1, 5}, {17, 3}, {300, 0x9e3779b97f4a7c15}}, func(b *nyanjit.Builder) {
		n, seed := b.Param(), b.Param()
		iv, hv := b.Var(b.Const(0)), b.Var(seed)
		loop, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		i, h := b.Get(iv), b.Get(hv)
		b.Branch(b.Compare(i, ir.BranchBelow, n), body)
		b.Jump(done)
		b.Bind(body)
		b.Set(iv, b.Add(i, b.Const(1)))
		b.Set(hv, b.Xor(b.RotateLeft(h, 13), i))
		b.Jump(loop)
		b.Bind(done)
		b.Return(b.Get(hv))
	})
	add("multiway", unary(64), func(b *nyanjit.Builder) {
		x := b.Param()
		key := b.And(x, b.Const(7))
		result := b.Var(b.Const(0))
		done := b.NewLabel()
		targets := make([]nyanjit.Label, 8)
		for k := range targets {
			targets[k] = b.NewLabel()
		}
		for k := 0; k < 7; k++ {
			b.Branch(b.CompareImmediate(key, ir.BranchEqual, int64(k)), targets[k])
		}
		b.Jump(targets[7])
		for k, label := range targets {
			b.Bind(label)
			b.Set(result, b.Mul(x, b.Const(uint64(k)*2+3)))
			b.Jump(done)
		}
		b.Bind(done)
		b.Return(b.Get(result))
	})
	add("critical-edges", binary(64), func(b *nyanjit.Builder) {
		// The edges l2→l1 and l2→l3 are critical: each Var holds what
		// its join receives, set before the branch on every path.
		x, y := b.Param(), b.Param()
		qv, pv, rv := b.Var(x), b.Var(y), b.Var(b.Const(0))
		l1, l2, l3 := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Branch(b.Compare(x, ir.BranchLess, y), l1)
		b.Jump(l2)
		b.Bind(l2)
		p := b.Get(pv)
		b.Set(qv, b.ShiftRight(p, 1))
		b.Set(rv, b.Add(p, b.Const(1)))
		b.Branch(b.CompareImmediate(p, ir.BranchAbove, 100), l1)
		b.Jump(l3)
		b.Bind(l3)
		b.Set(qv, b.Mul(b.Get(rv), b.Const(3)))
		b.Jump(l1)
		b.Bind(l1)
		b.Return(b.Xor(b.Get(qv), b.Const(0x55)))
	})
	add("irreducible", unary(64), func(b *nyanjit.Builder) {
		x := b.Param()
		av, cv, result := b.Var(x), b.Var(x), b.Var(b.Const(0))
		first, second, exit := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Branch(b.IsZero(b.And(x, b.Const(1))), first)
		b.Jump(second)
		b.Bind(first)
		a := b.Get(av)
		b.Set(result, b.Mul(a, b.Const(7)))
		b.Set(cv, b.Sub(a, b.Const(3)))
		b.Branch(b.CompareImmediate(a, ir.BranchBelow, 4), exit)
		b.Jump(second)
		b.Bind(second)
		c := b.Get(cv)
		b.Set(result, b.Mul(c, b.Const(11)))
		b.Set(av, b.ShiftRight(c, 1))
		b.Branch(b.CompareImmediate(c, ir.BranchBelow, 5), exit)
		b.Jump(first)
		b.Bind(exit)
		b.Return(b.Get(result))
	})
	add("scopes", binary(64), func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		b.Enter()
		shifted := b.Add(x, b.Const(2))
		b.Enter()
		inner := b.Var(b.Mul(shifted, y)) // captures shifted from the outer scope
		loop, done := b.NewLabel(), b.NewLabel()
		k := b.Var(b.Const(3))
		b.Jump(loop)
		b.Bind(loop)
		b.Branch(b.IsZero(b.Get(k)), done)
		b.Set(inner, b.Xor(b.Get(inner), b.Add(shifted, b.Get(k))))
		b.Set(k, b.Sub(b.Get(k), b.Const(1)))
		b.Jump(loop)
		b.Bind(done)
		result := b.Get(inner)
		b.Leave(result)
		scaled := b.Mul(result, b.Const(3))
		b.Leave(scaled)
		b.Return(b.Add(scaled, x))
	})
	add("wide-var", binary(64), func(b *nyanjit.Builder) {
		x, y := b.Param(), b.Param()
		v := b.Var(b.Concat(x, y))
		b.Set(v, b.Concat(b.Add(x, y), b.Xor(x, y)))
		got := b.Get(v)
		b.Return(b.Xor(b.ExtractBits(got, 0, 64), b.RotateLeft(b.ExtractBits(got, 64, 64), 7)))
	})
	for _, f := range []struct {
		name string
		call func(b *nyanjit.Builder, x nyanjit.Value) nyanjit.Value
	}{
		{"sin", (*nyanjit.Builder).FSin}, {"cos", (*nyanjit.Builder).FCos}, {"exp", (*nyanjit.Builder).FExp},
		{"log", (*nyanjit.Builder).FLog}, {"cbrt", (*nyanjit.Builder).FCbrt}, {"atan", (*nyanjit.Builder).FAtan},
		{"floor", (*nyanjit.Builder).FFloor}, {"round-even", (*nyanjit.Builder).FRoundToEven},
	} {
		for _, w := range []uint8{32, 64} {
			var inputs [][]uint64
			for _, x := range floatValues(w) {
				inputs = append(inputs, []uint64{x})
			}
			for _, x := range []float64{0.5, 2.75, 100.25, 1e6 + 0.5, 3.14159, -0.0001, 710, 88.5} {
				bits := math.Float64bits(x)
				if w == 32 {
					bits = uint64(math.Float32bits(float32(x)))
				}
				inputs = append(inputs, []uint64{bits})
			}
			add("math-"+f.name+"-"+itoa(w), inputs, func(b *nyanjit.Builder) {
				b.Return(f.call(b, b.ParamBits(w)))
			})
		}
	}
	add("math-pow", [][]uint64{
		{math.Float64bits(2), math.Float64bits(10)}, {math.Float64bits(1.5), math.Float64bits(-2.25)},
		{math.Float64bits(-8), math.Float64bits(3)}, {math.Float64bits(0), math.Float64bits(-1)},
	}, func(b *nyanjit.Builder) {
		b.Return(b.FPow(b.Param(), b.Param()))
	})
	vector := ir.Vec(4, 32)
	cases = append(cases, Case{Name: "control/vector-label-and-var", Buffer: vectorSize, Inputs: [][]uint64{{0}, {1}, {5}}, Build: built(func(b *nyanjit.Builder) {
		buffer, n := b.ParamNative(), b.Param()
		v := b.VLoad(vector, buffer)
		w := b.Var(b.VLoad(vector, b.Add(buffer, b.ConstNative(32))))
		iv, accv := b.Var(b.Const(0)), b.Var(v)
		loop, body, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
		b.Jump(loop)
		b.Bind(loop)
		i, acc := b.Get(iv), b.Get(accv)
		b.Branch(b.Compare(i, ir.BranchBelow, n), body)
		b.Jump(done)
		b.Bind(body)
		b.Set(w, b.Xor(b.Get(w), b.RotateLeft(acc, 5)))
		b.Set(iv, b.Add(i, b.Const(1)))
		b.Set(accv, b.Add(acc, b.Get(w)))
		b.Jump(loop)
		b.Bind(done)
		result := b.Get(accv)
		b.VStore(b.Add(buffer, b.ConstNative(64)), result)
		mask := b.VCompare(result, ir.BranchAbove, b.Get(w))
		b.Return(b.MaskBits(mask))
	})})
	return cases
}

func itoa[T int64 | uint8](x T) string { return strconv.FormatInt(int64(x), 10) }

var conditionName = [...]string{
	ir.BranchEqual: "eq", ir.BranchNotEqual: "ne", ir.BranchLess: "lt", ir.BranchGreaterEqual: "ge",
	ir.BranchLessEqual: "le", ir.BranchGreater: "gt", ir.BranchBelow: "below", ir.BranchAboveEqual: "above-eq",
	ir.BranchBelowEqual: "below-eq", ir.BranchAbove: "above",
}
