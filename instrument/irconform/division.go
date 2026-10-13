package irconform

import (
	"math"

	"github.com/moriyoshi/nyanjit/ir"
)

var divisionSemantics = []struct {
	name string
	s    ir.DivisionSemantics
}{{"zero", ir.DivisionZero}, {"allones", ir.DivisionAllOnes}, {"unchecked", ir.DivisionUnchecked}}

// divisionCases divides at every width with every semantics. The binary
// inputs include zero divisors and signed MIN / -1; unchecked divisions,
// which may trap on them, leave those out.
func divisionCases() []Case {
	var cases []Case
	for _, w := range widths {
		min := uint64(1) << (w - 1)
		for _, sem := range divisionSemantics {
			inputs := binary(w)
			if sem.s == ir.DivisionUnchecked {
				var checked [][]uint64
				for _, in := range inputs {
					if in[1] != 0 && !(in[0] == min && in[1] == mask(w)) {
						checked = append(checked, in)
					}
				}
				inputs = checked
			}
			for _, op := range []ir.Opcode{ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned} {
				cases = append(cases, Case{Name: name("integer", op, w, sem.name), Inputs: inputs,
					Build: straight(append(params(w, w), ir.Instruction{Op: op, Bits: w, A: 0, B: 1, Imm: uint64(sem.s)})...)})
			}
		}
	}
	return cases
}

var conversionSemantics = []struct {
	name string
	s    ir.ConversionSemantics
}{{"saturate", ir.ConversionSaturate}, {"native", ir.ConversionNative}}

// conversionCases converts between 32- and 64-bit integers and binary32 and
// binary64, with values that round (some only when rounded once, not
// through binary64), the integer extremes, and floats out of
// range, infinite or NaN.
func conversionCases() []Case {
	var cases []Case
	for _, in := range []uint8{32, 64} {
		integers := unary(in)
		for _, x := range []uint64{1<<24 + 1, 1<<53 + 1, 1<<63 + 1<<11 + 1, 0xfffffffffffff801, 0x7ffffffffffffc01, 1<<31 + 1, 0xffffff01,
			0xb017c8eea708, -0xb017c8eea708 & (1<<64 - 1), 1<<62 + 1<<38 + 1, -(1<<62 + 1<<38 + 1) & (1<<64 - 1)} {
			integers = append(integers, []uint64{x & mask(in)})
		}
		for _, out := range []uint8{32, 64} {
			for _, op := range []ir.Opcode{ir.OpFFromSigned, ir.OpFFromUnsigned} {
				cases = append(cases, Case{Name: name("float", op, in, out), Inputs: integers,
					Build: straight(append(params(in), ir.Instruction{Op: op, Bits: out, A: 0})...)})
			}
		}
	}
	for _, in := range []uint8{32, 64} {
		var floats [][]uint64
		for _, x := range floatValues(in) {
			floats = append(floats, []uint64{x})
		}
		for _, f := range []float64{-0.5, 0.999, -1, -1.5, 2147483647.5, -2147483648.5, -2147483649, 2147483648, 4294967295.5, 4294967296,
			9223372036854774784, 9223372036854775808, -9223372036854775808, -9223372036854777856, 18446744073709549568, 18446744073709551616,
			1e30, -1e30, math.Inf(1), math.Inf(-1), math.NaN(), -math.NaN()} {
			if in == 32 {
				floats = append(floats, []uint64{uint64(math.Float32bits(float32(f)))})
			} else {
				floats = append(floats, []uint64{math.Float64bits(f)})
			}
		}
		for _, out := range []uint8{32, 64} {
			for _, sem := range conversionSemantics {
				for _, op := range []ir.Opcode{ir.OpFToSigned, ir.OpFToUnsigned} {
					cases = append(cases, Case{Name: name("float", op, in, out, sem.name), Inputs: floats,
						Build: straight(append(params(in), ir.Instruction{Op: op, Bits: out, A: 0, Imm: uint64(sem.s)})...)})
				}
			}
		}
	}
	return cases
}
