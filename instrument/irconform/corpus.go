package irconform

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/moriyoshi/nyanjit/ir"
)

// nativeBits is the pointer width of the host, which addresses use.
const nativeBits = uint8(unsafe.Sizeof(uintptr(0)) * 8)

var widths = []uint8{8, 16, 32, 64}

func mask(bits uint8) uint64 {
	if bits >= 64 {
		return ^uint64(0)
	}
	return uint64(1)<<bits - 1
}

// edges returns boundary values at a width: zero, one, the signed extremes,
// all ones and two mixed patterns.
func edges(bits uint8) []uint64 {
	m := mask(bits)
	return []uint64{0, 1, 2, 7, m >> 1, m>>1 + 1, m - 1, m, 0x5a5a5a5a5a5a5a5a & m, 0x0123456789abcdef & m}
}

func unary(bits uint8) [][]uint64 {
	var inputs [][]uint64
	for _, x := range edges(bits) {
		inputs = append(inputs, []uint64{x})
	}
	return inputs
}

func binary(bits uint8) [][]uint64 {
	var inputs [][]uint64
	values := edges(bits)
	for i, x := range values {
		for _, y := range []uint64{0, 1, values[4], values[5], values[7], values[(i+3)%len(values)]} {
			inputs = append(inputs, []uint64{x, y})
		}
	}
	return inputs
}

func ternary(bits uint8) [][]uint64 {
	var inputs [][]uint64
	for _, pair := range binary(bits) {
		for _, z := range []uint64{0, 1, mask(bits)} {
			inputs = append(inputs, append(append([]uint64(nil), pair...), z))
		}
	}
	return inputs
}

// counts are shift counts around and beyond each width.
func counts(bits uint8) []uint64 {
	return []uint64{0, 1, uint64(bits) - 1, uint64(bits), uint64(bits) + 1, 63, 64, 65, 127, 128, 255, 256, 1 << 32, math.MaxUint64}
}

func params(bits ...uint8) []ir.Instruction {
	out := make([]ir.Instruction, len(bits))
	for i, b := range bits {
		out[i] = ir.Instruction{Op: ir.OpParam, Bits: b}
	}
	return out
}

// straight seals a program without control flow that returns its last value.
func straight(ins ...ir.Instruction) func() (*ir.SealedProgram, error) {
	return func() (*ir.SealedProgram, error) {
		arity := 0
		for _, i := range ins {
			if i.Op == ir.OpParam {
				arity++
			}
		}
		return ir.Seal(&ir.Program{Instructions: ins, Arity: arity, Result: len(ins) - 1})
	}
}

func name(group string, parts ...any) string {
	s := group
	for i, p := range parts {
		if i == 0 {
			s += "/"
		} else {
			s += "-"
		}
		s += fmt.Sprint(p)
	}
	return s
}

// Cases returns the conformance corpus. The result is a new slice.
func Cases() []Case {
	var cases []Case
	cases = append(cases, integerCases()...)
	cases = append(cases, shiftCases()...)
	cases = append(cases, floatCases()...)
	cases = append(cases, divisionCases()...)
	cases = append(cases, conversionCases()...)
	cases = append(cases, memoryCases()...)
	cases = append(cases, vectorCases()...)
	cases = append(cases, controlCases()...)
	cases = append(cases, crossBlockCases()...)
	cases = append(cases, paramCases()...)
	return cases
}

func integerCases() []Case {
	var cases []Case
	for _, w := range widths {
		for _, op := range []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpMulHigh, ir.OpMulHighSigned, ir.OpCarryAdd, ir.OpBorrowSub} {
			cases = append(cases, Case{Name: name("integer", op, w), Inputs: binary(w),
				Build: straight(append(params(w, w), ir.Instruction{Op: op, Bits: w, A: 0, B: 1})...)})
		}
		// A square reads one register as both inputs.
		for _, op := range []ir.Opcode{ir.OpMulHigh, ir.OpMulHighSigned} {
			cases = append(cases, Case{Name: name("integer", op, w, "square"), Inputs: unary(w),
				Build: straight(append(params(w), ir.Instruction{Op: op, Bits: w, A: 0, B: 0})...)})
		}
		for _, op := range []ir.Opcode{ir.OpAddWithCarry, ir.OpSubWithBorrow} {
			cases = append(cases, Case{Name: name("integer", op, w), Inputs: ternary(w),
				Build: straight(append(params(w, w, w), ir.Instruction{Op: op, Bits: w, A: 0, B: 1, C: 2})...)})
		}
		for _, op := range []ir.Opcode{ir.OpLeadingZeros, ir.OpTrailingZeros} {
			cases = append(cases, Case{Name: name("integer", op, w), Inputs: unary(w),
				Build: straight(append(params(w), ir.Instruction{Op: op, Bits: w, A: 0})...)})
		}
		cases = append(cases, Case{Name: name("integer", ir.OpSelect, w), Inputs: ternary(w),
			Build: straight(append(params(w, w, w), ir.Instruction{Op: ir.OpSelect, Bits: w, A: 2, B: 0, C: 1})...)})
		cases = append(cases, Case{Name: name("integer", "OpConst", w), Inputs: [][]uint64{{0}, {mask(w)}},
			Build: straight(append(params(w), ir.Instruction{Op: ir.OpConst, Bits: w, Imm: 0x8badf00ddeadbeef & mask(w)}, ir.Instruction{Op: ir.OpXor, Bits: w, A: 0, B: 1})...)})
		for _, from := range widths {
			cases = append(cases, Case{Name: name("integer", ir.OpCastUnsigned, from, w), Inputs: unary(from),
				Build: straight(append(params(from), ir.Instruction{Op: ir.OpCastUnsigned, Bits: w, A: 0})...)})
		}
		for _, extend := range []uint8{8, 16, 32} {
			if extend > w {
				continue
			}
			cases = append(cases, Case{Name: name("integer", ir.OpSignExtend, extend, w), Inputs: unary(w),
				Build: straight(append(params(w), ir.Instruction{Op: ir.OpSignExtend, Bits: w, A: 0, Imm: uint64(extend)})...)})
		}
	}
	for _, w := range addressWidths() {
		for _, scale := range []uint64{0, 1, 2, 3, 4, 8, 16} {
			cases = append(cases, Case{Name: name("integer", ir.OpAddScaled, w, scale), Inputs: binary(w),
				Build: straight(append(params(w, w), ir.Instruction{Op: ir.OpAddScaled, Bits: w, A: 0, B: 1, Imm: scale})...)})
		}
	}
	// Two-part results.
	cases = append(cases, Case{Name: "integer/result-parts", Inputs: binary(64), Build: func() (*ir.SealedProgram, error) {
		return ir.Seal(&ir.Program{Instructions: append(params(64, 64),
			ir.Instruction{Op: ir.OpMul, A: 0, B: 1}, ir.Instruction{Op: ir.OpMulHigh, Bits: 64, A: 0, B: 1}),
			Arity: 2, Result: 2, ResultParts: []int{2, 3}})
	}})
	return cases
}

func shiftCases() []Case {
	var cases []Case
	immediate := []ir.Opcode{ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight}
	byValue := []ir.Opcode{ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy}
	for _, w := range widths {
		for _, op := range immediate {
			for _, count := range []uint64{0, 1, uint64(w) / 2, uint64(w) - 1} {
				cases = append(cases, Case{Name: name("shift", op, w, count), Inputs: unary(w),
					Build: straight(append(params(w), ir.Instruction{Op: op, Bits: w, A: 0, Imm: count})...)})
			}
		}
		for _, op := range byValue {
			for _, countBits := range []uint8{8, 64} {
				var inputs [][]uint64
				for _, x := range edges(w) {
					for _, n := range counts(w) {
						inputs = append(inputs, []uint64{x, n & mask(countBits)})
					}
				}
				cases = append(cases, Case{Name: name("shift", op, w, countBits), Inputs: inputs,
					Build: straight(append(params(w, countBits), ir.Instruction{Op: op, Bits: w, A: 0, B: 1})...)})
			}
		}
	}
	return cases
}

var float64Inputs = []float64{0, math.Copysign(0, -1), 1, -1, 1.5, 0.1, 3, -7.25, 1e300, -1e300, 5e-324, -2.2250738585072014e-308,
	math.MaxFloat64, math.SmallestNonzeroFloat64, math.Inf(1), math.Inf(-1), math.NaN(), 9007199254740993, 0.3333333333333333}

var float32Inputs = []float32{0, float32(math.Copysign(0, -1)), 1, -1, 1.5, 0.1, 3, -7.25, 1e30, -1e30, 1e-45, -1.1754944e-38,
	math.MaxFloat32, math.SmallestNonzeroFloat32, float32(math.Inf(1)), float32(math.Inf(-1)), float32(math.NaN()), 16777217, 0.33333334}

func floatValues(bits uint8) []uint64 {
	var out []uint64
	if bits == 32 {
		for _, f := range float32Inputs {
			out = append(out, uint64(math.Float32bits(f)))
		}
		return out
	}
	for _, f := range float64Inputs {
		out = append(out, math.Float64bits(f))
	}
	return out
}

func floatCases() []Case {
	var cases []Case
	for _, w := range []uint8{32, 64} {
		values := floatValues(w)
		var pairs, triples, singles [][]uint64
		for i, x := range values {
			singles = append(singles, []uint64{x})
			for j, y := range values {
				pairs = append(pairs, []uint64{x, y})
				if (i+j)%3 == 0 {
					for _, z := range []uint64{values[0], values[3], values[i%len(values)], values[(j+5)%len(values)]} {
						triples = append(triples, []uint64{x, y, z})
					}
				}
			}
		}
		if w == 32 {
			// a*b+c lies just below a binary32 halfway point that binary64
			// rounds onto, so only a single rounding gives 1+2^-23.
			a, b, c := uint64(math.Float32bits(1+0x1p-23)), uint64(math.Float32bits(0x1p-24*(1-0x1p-23))), uint64(math.Float32bits(1+0x1p-23))
			triples = append(triples, []uint64{a, b, c}, []uint64{a | 1<<31, b, c | 1<<31})
		}
		for _, op := range []ir.Opcode{ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv} {
			cases = append(cases, Case{Name: name("float", op, w), Inputs: pairs,
				Build: straight(append(params(w, w), ir.Instruction{Op: op, Bits: w, A: 0, B: 1})...)})
		}
		cases = append(cases, Case{Name: name("float", ir.OpFMA, w), Inputs: triples,
			Build: straight(append(params(w, w, w), ir.Instruction{Op: ir.OpFMA, Bits: w, A: 0, B: 1, C: 2})...)})
		cases = append(cases, Case{Name: name("float", ir.OpFSqrt, w), Inputs: singles,
			Build: straight(append(params(w), ir.Instruction{Op: ir.OpFSqrt, Bits: w, A: 0})...)})
		cases = append(cases, Case{Name: name("float", ir.OpFConvert, 96-w, w), Inputs: unaryFloat(96 - w),
			Build: straight(append(params(96-w), ir.Instruction{Op: ir.OpFConvert, Bits: w, A: 0})...)})
		for c := ir.FloatEqual; c <= ir.FloatUnorderedEqual; c++ {
			cases = append(cases, Case{Name: name("float", ir.OpFCompare, w, uint8(c)), Inputs: pairs,
				Build: straight(append(params(w, w), ir.Instruction{Op: ir.OpFCompare, Bits: w, A: 0, B: 1, Imm: uint64(c)})...)})
		}
		// A contractible FAdd: targets with FMA may fuse the product.
		cases = append(cases, Case{Name: name("float", "contract", w), Inputs: triples,
			Build: straight(append(params(w, w, w), ir.Instruction{Op: ir.OpFMul, Bits: w, A: 0, B: 1},
				ir.Instruction{Op: ir.OpFAdd, Bits: w, A: 3, B: 2, Contract: true})...)})
	}
	// FProductError's contract: finite, well-scaled operands and C = round(A*B).
	var products [][]uint64
	for _, x := range []float64{1, -1.5, 0.1, 3, 1e10, -7.25, 0.3333333333333333, 123456.789} {
		for _, y := range []float64{1, 0.1, -3.7, 1e-10, 0.3333333333333333, 98765.4321} {
			products = append(products, []uint64{math.Float64bits(x), math.Float64bits(y)})
		}
	}
	cases = append(cases, Case{Name: "float/OpFProductError-64", Inputs: products,
		Build: straight(append(params(64, 64), ir.Instruction{Op: ir.OpFMul, Bits: 64, A: 0, B: 1},
			ir.Instruction{Op: ir.OpFProductError, Bits: 64, A: 0, B: 1, C: 2})...)})
	return cases
}

func unaryFloat(bits uint8) [][]uint64 {
	var out [][]uint64
	for _, x := range floatValues(bits) {
		out = append(out, []uint64{x})
	}
	// Values that round when narrowed.
	if bits == 64 {
		for _, f := range []float64{1.0000000596046448, 1.0000001192092896, 3.4028235677973366e38, 1.401298464324817e-45, 7.006492321624085e-46} {
			out = append(out, []uint64{math.Float64bits(f)})
		}
	}
	return out
}

const memorySize = 64

func memoryCases() []Case {
	var cases []Case
	type access struct {
		load, store ir.Opcode
		bits        uint8
	}
	accesses := []access{{ir.OpLoad8, ir.OpStore8, 8}, {ir.OpLoad16, ir.OpStore16, 16}, {ir.OpLoad32, ir.OpStore32, 32}, {ir.OpLoad64, ir.OpStore64, 64}}
	orders := []ir.ByteOrder{ir.ByteOrderNative, ir.ByteOrderLittleEndian, ir.ByteOrderBigEndian}
	for _, a := range accesses {
		for _, order := range orders {
			if a.bits == 64 && order == ir.ByteOrderBigEndian {
				continue
			}
			for _, aligned := range []bool{false, true} {
				if aligned && (a.bits == 8 || a.bits == 64) {
					continue
				}
				offsets := []uint64{0, 1, 3, 5, 8, 13}
				if aligned {
					offsets = []uint64{0, uint64(a.bits / 8), 16}
				}
				loadBits := []uint8{a.bits}
				if a.bits == 8 {
					loadBits = []uint8{8, 64}
				}
				for _, offset := range offsets {
					for _, lb := range loadBits {
						cases = append(cases, Case{Name: name("memory", a.load, lb, order, aligned, offset), Buffer: memorySize, Inputs: [][]uint64{{}},
							Build: straight(append(params(nativeBits),
								ir.Instruction{Op: ir.OpConst, Bits: nativeBits, Imm: offset},
								ir.Instruction{Op: ir.OpAdd, Bits: nativeBits, A: 0, B: 1},
								ir.Instruction{Op: a.load, Bits: lb, A: 2, ByteOrder: order, Aligned: aligned})...)})
					}
					valueBits := []uint8{a.bits}
					switch a.bits {
					case 8:
						valueBits = []uint8{8, 16, 32, 64}
					case 16, 32:
						valueBits = []uint8{a.bits, 64}
					}
					for _, vb := range valueBits {
						cases = append(cases, Case{Name: name("memory", a.store, vb, order, aligned, offset), Buffer: memorySize,
							Inputs: [][]uint64{{0x0123456789abcdef & mask(vb)}, {mask(vb)}, {0}},
							Build: straight(append(params(nativeBits, vb),
								ir.Instruction{Op: ir.OpConst, Bits: nativeBits, Imm: offset},
								ir.Instruction{Op: ir.OpAdd, Bits: nativeBits, A: 0, B: 2},
								ir.Instruction{Op: a.store, Bits: a.bits, A: 3, B: 1, ByteOrder: order, Aligned: aligned},
								ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 0})...)})
					}
				}
			}
		}
	}
	return cases
}

// addressWidths returns the widths AddScaled accepts: 64 and the native width.
func addressWidths() []uint8 {
	if nativeBits == 64 {
		return []uint8{64}
	}
	return []uint8{nativeBits, 64}
}
