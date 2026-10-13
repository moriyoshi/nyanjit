package irconform

import "github.com/moriyoshi/nyanjit"

// nanPayloads are NaNs with payloads and both signs, quiet and signaling,
// which sign operations must keep exactly.
func nanPayloads(bits uint8) []uint64 {
	if bits == 32 {
		return []uint64{0x7fc00123, 0xffc00123, 0x7f800001, 0xff812345}
	}
	return []uint64{0x7ff8000000000123, 0xfff8000000000123, 0x7ff0000000000001, 0xfff4000000012345}
}

// floatSignCases cover FNeg, FAbs and FCopySign on bit patterns, NaN
// payloads included, and on floating-point results that stay in FP
// registers.
func floatSignCases() []Case {
	var cases []Case
	add := func(name string, inputs [][]uint64, build func(b *nyanjit.Builder)) {
		cases = append(cases, Case{Name: name, Inputs: inputs, Build: built(build)})
	}
	for _, w := range []uint8{32, 64} {
		values := append(floatValues(w), nanPayloads(w)...)
		var singles, pairs [][]uint64
		for _, x := range values {
			singles = append(singles, []uint64{x})
			for _, y := range values {
				pairs = append(pairs, []uint64{x, y})
			}
		}
		add("float/fneg-"+itoa(w), singles, func(b *nyanjit.Builder) { b.Return(b.FNeg(b.ParamBits(w))) })
		add("float/fabs-"+itoa(w), singles, func(b *nyanjit.Builder) { b.Return(b.FAbs(b.ParamBits(w))) })
		add("float/fcopysign-"+itoa(w), pairs, func(b *nyanjit.Builder) {
			x, y := b.ParamBits(w), b.ParamBits(w)
			b.Return(b.FCopySign(x, y))
		})
		// Operands and results of float arithmetic stay in FP registers.
		add("float/fneg-resident-"+itoa(w), pairs, func(b *nyanjit.Builder) {
			x, y := b.ParamBits(w), b.ParamBits(w)
			b.Return(b.FAdd(b.FNeg(b.FMul(x, y)), y))
		})
		add("float/fabs-resident-"+itoa(w), pairs, func(b *nyanjit.Builder) {
			x, y := b.ParamBits(w), b.ParamBits(w)
			b.Return(b.FSub(b.FAbs(b.FSub(x, y)), x))
		})
		add("float/fcopysign-resident-"+itoa(w), pairs, func(b *nyanjit.Builder) {
			x, y := b.ParamBits(w), b.ParamBits(w)
			b.Return(b.FMul(b.FCopySign(b.FAdd(x, y), y), x))
		})
	}
	return cases
}
