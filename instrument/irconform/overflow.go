package irconform

import "github.com/moriyoshi/nyanjit"

// overflowCases cover Builder.Overflow of Add, Sub and Mul at every width,
// over operands at the signed limits, as a branch, as a value, and as a
// branch on a sum the program also returns.
func overflowCases() []Case {
	var cases []Case
	add := func(name string, inputs [][]uint64, build func(b *nyanjit.Builder)) {
		cases = append(cases, Case{Name: name, Inputs: inputs, Build: built(build)})
	}
	operations := []struct {
		name string
		op   func(b *nyanjit.Builder, x, y nyanjit.Value) nyanjit.Value
	}{
		{"add", (*nyanjit.Builder).Add}, {"sub", (*nyanjit.Builder).Sub}, {"mul", (*nyanjit.Builder).Mul},
	}
	for _, o := range operations {
		for _, w := range widths {
			add("control/overflow-"+o.name+"-"+itoa(w), binary(w), func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				b.Return(boolValue(b, b.Overflow(o.op(b, x, y))))
			})
			add("control/value-overflow-"+o.name+"-"+itoa(w), binary(w), func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				b.Return(b.PredicateValue(b.Overflow(o.op(b, x, y)), w))
			})
			// The branch reads the flags of the arithmetic whose result
			// the program returns: wrapped, or with the top bit flipped
			// on overflow.
			add("control/overflow-"+o.name+"-"+itoa(w)+"-result", binary(w), func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				result := b.Var(b.ConstBits(w, 0))
				r := o.op(b, x, y)
				b.Set(result, r)
				flagged, done := b.NewLabel(), b.NewLabel()
				b.Branch(b.Overflow(r).Not(), done)
				b.Jump(flagged)
				b.Bind(flagged)
				b.Set(result, b.Xor(b.Get(result), b.ConstBits(w, 1<<(w-1))))
				b.Jump(done)
				b.Bind(done)
				b.Return(b.Get(result))
			})
		}
	}
	return append(cases, overflowExitCases()...)
}

// overflowExitCases branch on overflow to a separate exit, without edge
// arguments, as a checked-arithmetic trap would.
func overflowExitCases() []Case {
	var cases []Case
	operations := []struct {
		name string
		op   func(b *nyanjit.Builder, x, y nyanjit.Value) nyanjit.Value
	}{
		{"add", (*nyanjit.Builder).Add}, {"sub", (*nyanjit.Builder).Sub}, {"mul", (*nyanjit.Builder).Mul},
	}
	for _, o := range operations {
		for _, w := range []uint8{32, 64} {
			cases = append(cases, Case{Name: "control/overflow-" + o.name + "-" + itoa(w) + "-exit", Inputs: binary(w), Build: built(func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				r := o.op(b, x, y)
				trap := b.NewLabel()
				b.Branch(b.Overflow(r), trap)
				b.Return(b.CastUnsigned(r, 64))
				b.Bind(trap)
				b.Return(b.Const(0xdead))
			})})
			// The arithmetic is used only by the check.
			cases = append(cases, Case{Name: "control/overflow-" + o.name + "-" + itoa(w) + "-check", Inputs: binary(w), Build: built(func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				trap := b.NewLabel()
				b.Branch(b.Overflow(o.op(b, x, y)), trap)
				b.Return(b.Const(1))
				b.Bind(trap)
				b.Return(b.Const(0xdead))
			})})
		}
	}
	return cases
}
