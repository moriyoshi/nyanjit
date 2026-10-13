package irconform

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

// floatABIValues are binary32 or binary64 inputs for float passing: the
// float corpus plus signaling NaNs with payloads and subnormals.
func floatABIValues(bits uint8) []uint64 {
	values := floatValues(bits)
	if bits == 32 {
		return append(values, 0x7f800001, 0xffa00123, 0x7fc00005, 0x00000001, 0x807fffff)
	}
	return append(values, 0x7ff0000000000001, 0xfff4000000000123, 0x7ff8000000000005, 0x0000000000000001, 0x800fffffffffffff)
}

// floatABICases are entries whose parameters and result travel in floating
// point registers (Case.Floats): identity, integer and float arguments
// mixed in both orders (independent register sequences; ppc64le C skips a
// GPR for each float), float results computed from integers, and a float
// result returned from two blocks.
func floatABICases() []Case {
	var cases []Case
	for _, w := range []uint8{32, 64} {
		result := abi.FloatResult64
		if w == 32 {
			result = abi.FloatResult32
		}
		values := floatABIValues(w)
		var singles, pairs, mixed [][]uint64
		for i, x := range values {
			singles = append(singles, []uint64{x})
			y := values[(i+5)%len(values)]
			pairs = append(pairs, []uint64{x, y})
			mixed = append(mixed, []uint64{uint64(i) * 0x10001, x, ^uint64(i), y})
		}
		cases = append(cases,
			Case{Name: name("float-abi", "identity", w), Inputs: singles, Floats: abi.FloatArgument(0) | result,
				Build: straight(params(w)...)},
			Case{Name: name("float-abi", "add", w), Inputs: pairs, Floats: abi.FloatArgument(0) | abi.FloatArgument(1) | result,
				Build: straight(append(params(w, w), ir.Instruction{Op: ir.OpFAdd, Bits: w, A: 0, B: 1})...)},
			// f(a int, x float, b int, y float) in each result kind.
			Case{Name: name("float-abi", "mixed-integers", w), Inputs: mixed, Floats: abi.FloatArgument(1) | abi.FloatArgument(3),
				Build: straight(append(params(64, w, 64, w), ir.Instruction{Op: ir.OpSub, Bits: 64, A: 0, B: 2})...)},
			Case{Name: name("float-abi", "mixed-floats", w), Inputs: mixed, Floats: abi.FloatArgument(1) | abi.FloatArgument(3) | result,
				Build: straight(append(params(64, w, 64, w), ir.Instruction{Op: ir.OpFSub, Bits: w, A: 1, B: 3})...)},
			Case{Name: name("float-abi", "mixed-last", w), Inputs: mixed, Floats: abi.FloatArgument(1) | abi.FloatArgument(3) | result,
				Build: straight(params(64, w, 64, w)...)},
			// f(x float, a int, y float, b int): the integers follow floats.
			Case{Name: name("float-abi", "floats-first", w), Inputs: swapped(mixed), Floats: abi.FloatArgument(0) | abi.FloatArgument(2),
				Build: straight(append(params(w, 64, w, 64), ir.Instruction{Op: ir.OpXor, Bits: 64, A: 1, B: 3})...)},
			Case{Name: name("float-abi", "result", w), Inputs: unary(64), Floats: result,
				Build: straight(append(params(64), ir.Instruction{Op: ir.OpFFromSigned, Bits: w, A: 0})...)},
			Case{Name: name("float-abi", "control", w), Inputs: pairs, Floats: abi.FloatArgument(0) | abi.FloatArgument(1) | result,
				Build: built(func(b *nyanjit.Builder) {
					x, y := b.ParamBits(w), b.ParamBits(w)
					less := b.NewLabel()
					b.Branch(b.FComparePredicate(x, ir.FloatLess, y), less)
					b.Return(b.FSub(x, y))
					b.Bind(less)
					b.Return(y)
				})},
		)
	}
	return cases
}

// swapped exchanges each input's first two and last two values.
func swapped(inputs [][]uint64) [][]uint64 {
	out := make([][]uint64, len(inputs))
	for i, in := range inputs {
		out[i] = []uint64{in[1], in[0], in[3], in[2]}
	}
	return out
}

// x87Quiets reports a 386 C float result that differs from want only as
// x87 FLD quiets a signaling NaN, the documented exception to bit-exact
// float passing there.
func x87Quiets(target compile.Target, floats abi.FloatPassing, want irinterp.Result, lo uint64) bool {
	if target.Architecture() != "386" || want.NaN || want.Unspecified {
		return false
	}
	switch floats.ResultBits() {
	case 32:
		x := uint32(want.Lo)
		return x&0x7f800000 == 0x7f800000 && x&0x007fffff != 0 && uint32(lo) == x|1<<22
	case 64:
		x := want.Lo
		return x&0x7ff0000000000000 == 0x7ff0000000000000 && x&0x000fffffffffffff != 0 && lo == x|1<<51
	}
	return false
}
