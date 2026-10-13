package irconform

import (
	"math"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// wordPairs are 64-bit operand pairs whose order is decided across the word
// boundary: equal high words with differing low words and the reverse.
var wordPairs = [][]uint64{
	{1 << 32, 1<<32 - 1}, {1<<32 - 1, 1 << 32}, {1<<32 | 1, 1 << 32}, {1 << 32, 1<<32 | 1},
	{0xffffffff00000000, 0xffffffffffffffff}, {0xffffffffffffffff, 0xffffffff00000000},
	{0x8000000000000000, 0x7fffffffffffffff}, {0x7fffffffffffffff, 0x8000000000000000},
	{0x80000000ffffffff, 0x8000000100000000}, {0x00000001ffffffff, 0xffffffff00000001},
	{0x123456789abcdef0, 0x123456789abcdef0}, {0x123456789abcdef0, 0x123456799abcdef0},
}

// compareCases cover OpCompare at every width and condition, its select
// fusion, and the value forms CompareValue and PredicateValue: each
// control/compare-* case has a control/value-* twin that returns the
// predicate's value instead of merging it through a Var.
func compareCases() []Case {
	var cases []Case
	for c := ir.BranchEqual; c <= ir.BranchAbove; c++ {
		for _, w := range widths {
			cases = append(cases, Case{Name: "integer/compare-" + conditionName[c] + "-" + itoa(w), Inputs: binary(w),
				Build: straight(append(params(w, w), ir.Instruction{Op: ir.OpCompare, Bits: w, A: 0, B: 1, Imm: uint64(c)})...)})
			// A select on a single-use comparison selects from its flags.
			cases = append(cases, Case{Name: "integer/select-compare-" + conditionName[c] + "-" + itoa(w), Inputs: binary(w),
				Build: straight(append(params(w, w), ir.Instruction{Op: ir.OpCompare, Bits: w, A: 0, B: 1, Imm: uint64(c)},
					ir.Instruction{Op: ir.OpSelect, Bits: w, A: 2, B: 0, C: 1})...)})
		}
		cases = append(cases, Case{Name: "integer/compare-" + conditionName[c] + "-64-words", Inputs: wordPairs,
			Build: straight(append(params(64, 64), ir.Instruction{Op: ir.OpCompare, Bits: 64, A: 0, B: 1, Imm: uint64(c)})...)})
		cases = append(cases, Case{Name: "integer/select-compare-" + conditionName[c] + "-64-words", Inputs: wordPairs,
			Build: straight(append(params(64, 64), ir.Instruction{Op: ir.OpCompare, Bits: 64, A: 0, B: 1, Imm: uint64(c)},
				ir.Instruction{Op: ir.OpSelect, Bits: 64, A: 2, B: 0, C: 1})...)})
		// A narrow comparison selecting wider values.
		cases = append(cases, Case{Name: "integer/select-compare-" + conditionName[c] + "-8-64", Inputs: binary(8),
			Build: straight(append(params(8, 8), ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: 0}, ir.Instruction{Op: ir.OpConst, Imm: 0x8000000000000001},
				ir.Instruction{Op: ir.OpCompare, Bits: 8, A: 0, B: 1, Imm: uint64(c)}, ir.Instruction{Op: ir.OpSelect, Bits: 64, A: 4, B: 2, C: 3})...)})
	}
	add := func(name string, inputs [][]uint64, build func(b *nyanjit.Builder)) {
		cases = append(cases, Case{Name: name, Inputs: inputs, Build: built(build)})
	}
	for c := ir.BranchEqual; c <= ir.BranchAbove; c++ {
		add("integer/compare-value-"+conditionName[c]+"-128", wideCompareInputs(), func(b *nyanjit.Builder) {
			x := b.Concat(b.Param(), b.Param())
			y := b.Concat(b.Param(), b.Param())
			b.Return(b.CompareValue(x, c, y))
		})
		add("control/value-compare-"+conditionName[c], binary(64), func(b *nyanjit.Builder) {
			x, y := b.Param(), b.Param()
			b.Return(b.PredicateValue(b.Compare(x, c, y), 64))
		})
		for _, w := range []uint8{8, 16, 32} {
			add("control/value-compare-"+conditionName[c]+"-"+itoa(w), binary(w), func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				b.Return(b.PredicateValue(b.Compare(x, c, y), w))
			})
		}
		for _, imm := range []int64{-1, 0, 5, math.MinInt32, 4096, 1 << 40} {
			add("control/value-compare-immediate-"+conditionName[c]+"-"+itoa(imm), unary(64), func(b *nyanjit.Builder) {
				b.Return(b.PredicateValue(b.CompareImmediate(b.Param(), c, imm), 64))
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
			add("control/value-fcompare-"+itoa(uint8(c))+"-"+itoa(w), pairs, func(b *nyanjit.Builder) {
				x, y := b.ParamBits(w), b.ParamBits(w)
				b.Return(b.PredicateValue(b.FComparePredicate(x, c, y), w))
			})
		}
		add("control/value-fcompare-tree-"+itoa(w), pairs, func(b *nyanjit.Builder) {
			x, y := b.ParamBits(w), b.ParamBits(w)
			p := b.FComparePredicate(x, ir.FloatLess, y).Or(b.FComparePredicate(x, ir.FloatUnordered, x).Not().
				And(b.CompareImmediate(y, ir.BranchBelow, 1<<20)))
			b.Return(b.PredicateValue(p, 64))
		})
	}
	add("control/value-carry", binary(64), func(b *nyanjit.Builder) {
		sum := b.Add(b.Param(), b.Param())
		b.Return(b.Add(b.PredicateValue(b.Carry(sum), 64), sum))
	})
	for _, w := range widths {
		add("control/value-predicate-tree-"+itoa(w), ternary(64), func(b *nyanjit.Builder) {
			x, y, z := b.Param(), b.Param(), b.Param()
			p := b.Compare(x, ir.BranchLess, y).And(b.CompareImmediate(z, ir.BranchNotEqual, 0)).
				Or(b.Compare(y, ir.BranchBelow, x).Not().And(b.IsNegative(z))).
				Or(b.IsZero(x).And(b.IsZero(y).Not()))
			b.Return(b.PredicateValue(p, w))
		})
	}
	// Both forms of one predicate in one program; their difference is 0.
	add("control/value-and-branch", ternary(64), func(b *nyanjit.Builder) {
		x, y, z := b.Param(), b.Param(), b.Param()
		p := b.Compare(x, ir.BranchGreater, y).Or(b.Compare(z, ir.BranchAboveEqual, x).And(b.IsZero(y).Not()))
		value := b.PredicateValue(p, 64)
		b.Return(b.Add(b.Xor(value, boolValue(b, p)), b.ShiftLeft(value, 8)))
	})
	add("control/value-select", ternary(64), func(b *nyanjit.Builder) {
		x, y, z := b.Param(), b.Param(), b.Param()
		b.Return(b.Select(b.PredicateValue(b.Compare(x, ir.BranchLess, y), 64), z, b.Xor(x, y)))
	})
	add("control/value-empty", unary(64), func(b *nyanjit.Builder) {
		b.Return(b.Add(b.Param(), b.PredicateValue(nyanjit.Predicate{}, 64)))
	})
	return cases
}

// wideCompareInputs are 128-bit operand pairs, as four words, whose high
// words are equal, ordered either way, or signed and unsigned opposites.
func wideCompareInputs() [][]uint64 {
	var inputs [][]uint64
	highs := [][2]uint64{{0, 0}, {1, 0}, {0, 1}, {1 << 63, 1<<63 - 1}, {1<<63 - 1, 1 << 63}, {^uint64(0), ^uint64(0)}}
	lows := [][2]uint64{{0, 0}, {1, 2}, {2, 1}, {^uint64(0), 0}, {0, ^uint64(0)}}
	for _, h := range highs {
		for _, l := range lows {
			inputs = append(inputs, []uint64{l[0], h[0], l[1], h[1]})
		}
	}
	return inputs
}
