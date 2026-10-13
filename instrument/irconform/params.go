package irconform

import (
	"math"

	"github.com/moriyoshi/nyanjit/ir"
)

// paramCases are raw-IR programs built with ir.ProgramBuilder that pass
// values on edges to block parameters. Builders never produce block
// parameters, but raw IR does, and compile threads cross-block values through
// parameters it creates itself, so these keep the edge-argument lowering
// (two-way edges with their own arguments, loop-carried values, and parallel
// copies) under conformance.
func paramCases() []Case {
	i64 := ir.Scalar(64)
	add := func(pb *ir.ProgramBuilder, b ir.Block, op ir.Opcode, bits uint8, x, y int) int {
		return pb.Append(b, ir.Instruction{Op: op, Bits: bits, A: x, B: y})
	}
	shift := func(pb *ir.ProgramBuilder, b ir.Block, op ir.Opcode, x int, count uint64) int {
		return pb.Append(b, ir.Instruction{Op: op, Bits: 64, A: x, Imm: count})
	}
	constant := func(pb *ir.ProgramBuilder, b ir.Block, bits uint8, x uint64) int {
		return pb.Append(b, ir.Instruction{Op: ir.OpConst, Bits: bits, Imm: x})
	}
	isZero := func(x int) *ir.PredicateExpr {
		return &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: x}
	}
	// count bounds a loop by the low four bits of k.
	count := func(pb *ir.ProgramBuilder, entry ir.Block, k int) int {
		return add(pb, entry, ir.OpAnd, 64, k, constant(pb, entry, 64, 15))
	}
	loopInputs := [][]uint64{{0, 0, 0}, {1, 2, 3}, {5, 0x8000000000000000, 7}, {14, ^uint64(0), 0x0123456789abcdef}, {15, 0x55, 0xaa}, {3, 1 << 40, 9}}
	var cases []Case
	built := func(name string, inputs [][]uint64, build func(pb *ir.ProgramBuilder)) {
		cases = append(cases, Case{Name: "params/" + name, Inputs: inputs, Build: func() (*ir.SealedProgram, error) {
			pb := ir.NewProgramBuilder()
			build(pb)
			return pb.Seal()
		}})
	}

	// Both edges of one branch reach the same block with different
	// arguments, so each edge needs its own copies.
	built("branch-same-target", binary(64), func(pb *ir.ProgramBuilder) {
		entry := pb.Entry()
		x, y := pb.Param(entry, i64), pb.Param(entry, i64)
		join := pb.NewBlock(i64, i64)
		sum, diff := add(pb, entry, ir.OpAdd, 64, x, y), add(pb, entry, ir.OpSub, 64, y, x)
		pb.Branch(entry, compareValues(ir.BranchLess, x, y), join, []int{sum, x}, join, []int{diff, add(pb, entry, ir.OpXor, 64, x, y)})
		p, q := pb.BlockParam(join, 0), pb.BlockParam(join, 1)
		pb.Return(join, add(pb, join, ir.OpXor, 64, add(pb, join, ir.OpMul, 64, p, constant(pb, join, 64, 3)), q))
	})
	// Each target takes a different number of arguments.
	built("branch-two-targets", binary(64), func(pb *ir.ProgramBuilder) {
		entry := pb.Entry()
		x, y := pb.Param(entry, i64), pb.Param(entry, i64)
		one, two := pb.NewBlock(i64), pb.NewBlock(i64, i64, i64)
		pb.Branch(entry, compareValues(ir.BranchBelow, y, x), one, []int{y}, two, []int{y, x, y})
		pb.Return(one, shift(pb, one, ir.OpRotateLeft, pb.BlockParam(one, 0), 9))
		a, b, c := pb.BlockParam(two, 0), pb.BlockParam(two, 1), pb.BlockParam(two, 2)
		pb.Return(two, add(pb, two, ir.OpSub, 64, add(pb, two, ir.OpMul, 64, a, b), shift(pb, two, ir.OpShiftRight, c, 3)))
	})
	// A loop carrying its counter and accumulator, with the invariant
	// used directly by dominance.
	built("loop", loopInputs, func(pb *ir.ProgramBuilder) {
		entry := pb.Entry()
		k, seed, step := pb.Param(entry, i64), pb.Param(entry, i64), pb.Param(entry, i64)
		n := count(pb, entry, k)
		head, body, exit := pb.NewBlock(i64, i64), pb.NewBlock(), pb.NewBlock(i64)
		pb.Jump(entry, head, constant(pb, entry, 64, 0), seed)
		i, acc := pb.BlockParam(head, 0), pb.BlockParam(head, 1)
		pb.Branch(head, compareValues(ir.BranchBelow, i, n), body, nil, exit, []int{acc})
		next := add(pb, body, ir.OpAdd, 64, add(pb, body, ir.OpMul, 64, acc, constant(pb, body, 64, 31)), add(pb, body, ir.OpXor, 64, i, step))
		pb.Jump(body, head, add(pb, body, ir.OpAdd, 64, i, constant(pb, body, 64, 1)), next)
		pb.Return(exit, pb.BlockParam(exit, 0))
	})
	// A self loop whose taken edge swaps its parameters: the edge is
	// critical, and its copies form a two-cycle.
	built("swap", loopInputs, func(pb *ir.ProgramBuilder) {
		entry := pb.Entry()
		k, a0, b0 := pb.Param(entry, i64), pb.Param(entry, i64), pb.Param(entry, i64)
		head, exit := pb.NewBlock(i64, i64, i64), pb.NewBlock(i64, i64)
		pb.Jump(entry, head, a0, b0, count(pb, entry, k))
		a, b, n := pb.BlockParam(head, 0), pb.BlockParam(head, 1), pb.BlockParam(head, 2)
		// The swapped values are also perturbed so each iteration counts.
		bumped := add(pb, head, ir.OpAdd, 64, b, n)
		less := add(pb, head, ir.OpSub, 64, n, constant(pb, head, 64, 1))
		pb.Branch(head, isZero(n), exit, []int{a, b}, head, []int{bumped, a, less})
		x, y := pb.BlockParam(exit, 0), pb.BlockParam(exit, 1)
		pb.Return(exit, add(pb, exit, ir.OpXor, 64, add(pb, exit, ir.OpMul, 64, x, constant(pb, exit, 64, 3)), y))
	})
	// A pure swap, with no other work, on the back edge.
	built("swap-pure", loopInputs, func(pb *ir.ProgramBuilder) {
		entry := pb.Entry()
		k, a0, b0 := pb.Param(entry, i64), pb.Param(entry, i64), pb.Param(entry, i64)
		head, body, exit := pb.NewBlock(i64, i64, i64), pb.NewBlock(), pb.NewBlock()
		pb.Jump(entry, head, a0, b0, count(pb, entry, k))
		a, b, n := pb.BlockParam(head, 0), pb.BlockParam(head, 1), pb.BlockParam(head, 2)
		pb.Branch(head, isZero(n), exit, nil, body, nil)
		pb.Jump(body, head, b, a, add(pb, body, ir.OpSub, 64, n, constant(pb, body, 64, 1)))
		pb.Return(exit, add(pb, exit, ir.OpSub, 64, a, shift(pb, exit, ir.OpShiftLeft, b, 1)))
	})
	// A three-cycle rotation through a body block.
	built("rotate3", loopInputs, func(pb *ir.ProgramBuilder) {
		entry := pb.Entry()
		k, a0, b0 := pb.Param(entry, i64), pb.Param(entry, i64), pb.Param(entry, i64)
		c0 := add(pb, entry, ir.OpXor, 64, a0, b0)
		head, body, exit := pb.NewBlock(i64, i64, i64, i64), pb.NewBlock(), pb.NewBlock()
		pb.Jump(entry, head, a0, b0, c0, count(pb, entry, k))
		a, b, c, n := pb.BlockParam(head, 0), pb.BlockParam(head, 1), pb.BlockParam(head, 2), pb.BlockParam(head, 3)
		pb.Branch(head, isZero(n), exit, nil, body, nil)
		pb.Jump(body, head, b, c, add(pb, body, ir.OpAdd, 64, a, constant(pb, body, 64, 1)), add(pb, body, ir.OpSub, 64, n, constant(pb, body, 64, 1)))
		r := add(pb, exit, ir.OpXor, 64, add(pb, exit, ir.OpMul, 64, a, constant(pb, exit, 64, 5)), shift(pb, exit, ir.OpRotateLeft, b, 17))
		pb.Return(exit, add(pb, exit, ir.OpSub, 64, r, c))
	})
	// The back edge passes one value to two parameters, forwards a
	// parameter to another, and keeps one in place.
	built("duplicate-and-forward", loopInputs, func(pb *ir.ProgramBuilder) {
		entry := pb.Entry()
		k, a0, b0 := pb.Param(entry, i64), pb.Param(entry, i64), pb.Param(entry, i64)
		head, body, exit := pb.NewBlock(i64, i64, i64, i64), pb.NewBlock(), pb.NewBlock()
		pb.Jump(entry, head, a0, a0, b0, count(pb, entry, k))
		a, b, c, n := pb.BlockParam(head, 0), pb.BlockParam(head, 1), pb.BlockParam(head, 2), pb.BlockParam(head, 3)
		pb.Branch(head, isZero(n), exit, nil, body, nil)
		mixed := add(pb, body, ir.OpAdd, 64, add(pb, body, ir.OpXor, 64, a, c), n)
		pb.Jump(body, head, c, mixed, mixed, add(pb, body, ir.OpSub, 64, n, constant(pb, body, 64, 1)))
		r := add(pb, exit, ir.OpAdd, 64, add(pb, exit, ir.OpMul, 64, a, constant(pb, exit, 64, 7)), add(pb, exit, ir.OpMul, 64, b, constant(pb, exit, 64, 11)))
		pb.Return(exit, add(pb, exit, ir.OpXor, 64, r, c))
	})
	// More loop-carried parameters than any target has registers for,
	// rotated by one on every iteration.
	built("rotate-many", loopInputs, func(pb *ir.ProgramBuilder) {
		const width = 20
		entry := pb.Entry()
		k, a0, b0 := pb.Param(entry, i64), pb.Param(entry, i64), pb.Param(entry, i64)
		types := make([]ir.Type, width+1)
		for i := range types {
			types[i] = i64
		}
		head, body, exit := pb.NewBlock(types...), pb.NewBlock(), pb.NewBlock()
		initial := make([]int, width+1)
		for i := range width {
			initial[i] = add(pb, entry, ir.OpAdd, 64, add(pb, entry, ir.OpMul, 64, a0, constant(pb, entry, 64, uint64(i+1))), b0)
		}
		initial[width] = count(pb, entry, k)
		pb.Jump(entry, head, initial...)
		ps := make([]int, width+1)
		for i := range ps {
			ps[i] = pb.BlockParam(head, i)
		}
		n := ps[width]
		pb.Branch(head, isZero(n), exit, nil, body, nil)
		next := make([]int, width+1)
		for i := range width - 1 {
			next[i] = ps[i+1]
		}
		next[width-1] = add(pb, body, ir.OpXor, 64, ps[0], n)
		next[width] = add(pb, body, ir.OpSub, 64, n, constant(pb, body, 64, 1))
		pb.Jump(body, head, next...)
		r := ps[0]
		for i := 1; i < width; i++ {
			op := ir.OpAdd
			if i%2 == 1 {
				op = ir.OpXor
			}
			r = add(pb, exit, op, 64, shift(pb, exit, ir.OpRotateLeft, r, 7), ps[i])
		}
		pb.Return(exit, r)
	})
	// Narrow parameters wrap at their width.
	for _, w := range []uint8{8, 16, 32} {
		var inputs [][]uint64
		for _, x := range edges(w) {
			inputs = append(inputs, []uint64{x, x ^ 0x5a})
		}
		built("narrow-"+itoa(w), inputs, func(pb *ir.ProgramBuilder) {
			t := ir.Scalar(w)
			entry := pb.Entry()
			x, y := pb.Param(entry, t), pb.Param(entry, t)
			head, body, exit := pb.NewBlock(t, t, t), pb.NewBlock(), pb.NewBlock(t)
			pb.Jump(entry, head, x, y, constant(pb, entry, w, 5))
			a, b, n := pb.BlockParam(head, 0), pb.BlockParam(head, 1), pb.BlockParam(head, 2)
			pb.Branch(head, isZero(n), exit, []int{add(pb, head, ir.OpXor, w, a, b)}, body, nil)
			grown := add(pb, body, ir.OpAdd, w, add(pb, body, ir.OpMul, w, a, constant(pb, body, w, 0x9b)), b)
			pb.Jump(body, head, b, grown, add(pb, body, ir.OpSub, w, n, constant(pb, body, w, 1)))
			pb.Return(exit, pb.BlockParam(exit, 0))
		})
	}
	// Floating-point values carried in parameters, swapped on the back
	// edge, and merged from two edges.
	for _, w := range []uint8{32, 64} {
		var inputs [][]uint64
		values := floatValues(w)
		for i, x := range values {
			inputs = append(inputs, []uint64{x, values[(i+3)%len(values)]})
		}
		one := math.Float64bits(1.5)
		if w == 32 {
			one = uint64(math.Float32bits(1.5))
		}
		built("float-loop-"+itoa(w), inputs, func(pb *ir.ProgramBuilder) {
			t := ir.Scalar(w)
			entry := pb.Entry()
			x, y := pb.Param(entry, t), pb.Param(entry, t)
			head, body, exit := pb.NewBlock(t, t, i64), pb.NewBlock(), pb.NewBlock(t)
			pb.Jump(entry, head, x, y, constant(pb, entry, 64, 4))
			f, g, n := pb.BlockParam(head, 0), pb.BlockParam(head, 1), pb.BlockParam(head, 2)
			pb.Branch(head, isZero(n), exit, []int{add(pb, head, ir.OpFSub, w, f, g)}, body, nil)
			scaled := add(pb, body, ir.OpFAdd, w, add(pb, body, ir.OpFMul, w, f, constant(pb, body, w, one)), g)
			pb.Jump(body, head, g, scaled, add(pb, body, ir.OpSub, 64, n, constant(pb, body, 64, 1)))
			pb.Return(exit, pb.BlockParam(exit, 0))
		})
		built("float-merge-"+itoa(w), inputs, func(pb *ir.ProgramBuilder) {
			t := ir.Scalar(w)
			entry := pb.Entry()
			x, y := pb.Param(entry, t), pb.Param(entry, t)
			join := pb.NewBlock(t, t)
			sum, product := add(pb, entry, ir.OpFAdd, w, x, y), add(pb, entry, ir.OpFMul, w, x, y)
			less := pb.Append(entry, ir.Instruction{Op: ir.OpFCompare, Bits: w, A: x, B: y, Imm: uint64(ir.FloatLess)})
			pb.Branch(entry, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchNotEqual, Value: less},
				join, []int{sum, product}, join, []int{y, x})
			pb.Return(join, add(pb, join, ir.OpFDiv, w, pb.BlockParam(join, 0), pb.BlockParam(join, 1)))
		})
	}
	// A 128-bit value carried as two 64-bit parameters, returned as a
	// pair.
	built("wide-pair", loopInputs, func(pb *ir.ProgramBuilder) {
		entry := pb.Entry()
		k, lo0, hi0 := pb.Param(entry, i64), pb.Param(entry, i64), pb.Param(entry, i64)
		head, body, exit := pb.NewBlock(i64, i64, i64), pb.NewBlock(), pb.NewBlock()
		pb.Jump(entry, head, lo0, hi0, count(pb, entry, k))
		lo, hi, n := pb.BlockParam(head, 0), pb.BlockParam(head, 1), pb.BlockParam(head, 2)
		pb.Branch(head, isZero(n), exit, nil, body, nil)
		nextLo := add(pb, body, ir.OpXor, 64, hi, shift(pb, body, ir.OpShiftLeft, lo, 1))
		pb.Jump(body, head, nextLo, add(pb, body, ir.OpAdd, 64, lo, hi), add(pb, body, ir.OpSub, 64, n, constant(pb, body, 64, 1)))
		pb.Return(exit, lo, hi)
	})
	return cases
}
