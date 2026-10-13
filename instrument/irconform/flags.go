package irconform

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// flagCases branch on the result of arithmetic right after it, so a target
// may decide the branch from the flags the arithmetic left: against zero,
// on the carry or borrow, after an INC or DEC that keeps the carry, on a
// narrowed or sign-extended result, and on 64-bit sums and differences,
// which 386 forms as register pairs. Each branch returns a value that
// depends on its side.
func flagCases() []Case {
	var cases []Case
	add := func(name string, inputs [][]uint64, build func(b *nyanjit.Builder)) {
		cases = append(cases, Case{Name: "flags/" + name, Inputs: inputs, Build: built(build)})
	}
	// branch returns r when p holds and r ^ 0x5a otherwise.
	branch := func(b *nyanjit.Builder, p nyanjit.Predicate, r nyanjit.Value, w uint8) {
		yes := b.NewLabel()
		b.Branch(p, yes)
		b.Return(b.Xor(r, b.ConstBits(w, 0x5a)))
		b.Bind(yes)
		b.Return(r)
	}
	operations := []struct {
		name string
		op   func(b *nyanjit.Builder, x, y nyanjit.Value) nyanjit.Value
	}{
		{"add", (*nyanjit.Builder).Add}, {"sub", (*nyanjit.Builder).Sub}, {"and", (*nyanjit.Builder).And},
		{"or", (*nyanjit.Builder).Or}, {"xor", (*nyanjit.Builder).Xor}, {"mul", (*nyanjit.Builder).Mul},
	}
	zeroConditions := []ir.BranchCondition{ir.BranchEqual, ir.BranchNotEqual, ir.BranchLess, ir.BranchGreaterEqual, ir.BranchLessEqual, ir.BranchGreater}
	for _, w := range widths {
		for _, o := range operations {
			for _, c := range zeroConditions {
				add(o.name+"-"+itoa(w)+"-zero-"+conditionName[c], binary(w), func(b *nyanjit.Builder) {
					x, y := b.ParamBits(w), b.ParamBits(w)
					r := o.op(b, x, y)
					branch(b, b.CompareImmediate(r, c, 0), r, w)
				})
			}
		}
		for _, c := range []ir.BranchCondition{ir.BranchBelow, ir.BranchAboveEqual} {
			// The carry of x + y, and the borrow of x - y.
			add("add-"+itoa(w)+"-carry-"+conditionName[c], binary(w), func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				s := b.Add(x, y)
				branch(b, b.Compare(s, c, x), s, w)
			})
			add("sub-"+itoa(w)+"-borrow-"+conditionName[c], binary(w), func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				s := b.Sub(x, y)
				branch(b, b.Compare(x, c, y), s, w)
			})
			// An increment or decrement between the arithmetic and the
			// branch: x86 INC and DEC keep the carry.
			add("add-"+itoa(w)+"-inc-carry-"+conditionName[c], binary(w), func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				s := b.Add(x, y)
				t := b.Add(s, b.ConstBits(w, 1))
				branch(b, b.Compare(s, c, x), t, w)
			})
			add("sub-"+itoa(w)+"-dec-borrow-"+conditionName[c], binary(w), func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				s := b.Sub(x, y)
				t := b.Sub(s, b.ConstBits(w, 1))
				branch(b, b.Compare(x, c, y), t, w)
			})
		}
		for _, c := range zeroConditions {
			add("inc-"+itoa(w)+"-zero-"+conditionName[c], unary(w), func(b *nyanjit.Builder) {
				t := b.Add(b.ParamBits(w), b.ConstBits(w, 1))
				branch(b, b.CompareImmediate(t, c, 0), t, w)
			})
			add("dec-"+itoa(w)+"-zero-"+conditionName[c], unary(w), func(b *nyanjit.Builder) {
				t := b.Sub(b.ParamBits(w), b.ConstBits(w, 1))
				branch(b, b.CompareImmediate(t, c, 0), t, w)
			})
		}
		for _, c := range []ir.BranchCondition{ir.BranchEqual, ir.BranchLess, ir.BranchGreater} {
			// A narrowed sum, and a sum sign-extended to 64 bits.
			for _, n := range widths {
				if n < w {
					add("add-"+itoa(w)+"-narrow-"+itoa(n)+"-"+conditionName[c], binary(w), func(b *nyanjit.Builder) {
						x, y := b.ParamBits(w), b.ParamBits(w)
						r := b.CastUnsigned(b.Add(x, y), n)
						branch(b, b.CompareImmediate(r, c, 0), r, n)
					})
				}
			}
			if w < 64 {
				add("add-"+itoa(w)+"-extend-"+conditionName[c], binary(w), func(b *nyanjit.Builder) {
					x, y := b.ParamBits(w), b.ParamBits(w)
					r := b.SignExtend(b.Add(x, y), w)
					branch(b, b.CompareImmediate(r, c, 0), r, 64)
				})
			}
		}
	}
	return cases
}
