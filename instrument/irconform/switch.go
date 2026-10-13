package irconform

import (
	"strconv"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// switchCases cover the bounded Switch terminator: case counts on both sides
// of the lowering thresholds, dense, sparse-with-duplicates and all-same
// targets, selectors at the bounds and beyond at every width (on 386, a
// 64-bit selector with a nonzero high word), raw IR with block parameters on
// every edge, and threaded dispatch in a loop.
func switchCases() []Case {
	var cases []Case
	add := func(name string, inputs [][]uint64, build func() (*ir.SealedProgram, error)) {
		cases = append(cases, Case{Name: "control/switch-" + name, Inputs: inputs, Build: build})
	}
	for _, n := range []int{1, 2, 3, 7, 8, 64, 1000} {
		for _, pattern := range []string{"dense", "sparse", "same"} {
			for _, w := range []uint8{8, 16, 32, 64} {
				add(pattern+"-"+strconv.Itoa(n)+"-"+strconv.Itoa(int(w)), switchInputs(n, w), built(func(b *nyanjit.Builder) {
					switchBody(b, n, pattern, w)
				}))
			}
		}
	}
	add("loop", [][]uint64{{0, 1}, {1, 2}, {5, 0x9e3779b97f4a7c15}, {40, 3}, {63, ^uint64(0)}, {17, 1 << 40}}, built(switchLoop))
	add("params", [][]uint64{{0, 3, 5}, {1, 3, 5}, {2, 7, 1}, {3, 1 << 33, 9}, {4, 6, 6}, {1 << 32, 2, 3}, {^uint64(0), 8, 1}}, switchParams)
	add("params-loop", [][]uint64{{0, 1}, {3, 2}, {9, 0x55}, {15, ^uint64(0)}}, switchParamsLoop)
	var tableInputs [][]uint64
	for _, s := range []uint64{0, 1, 5, 11, 12, 13, 200, 255} {
		tableInputs = append(tableInputs, []uint64{s, 0x0123456789abcdef, 77})
	}
	add("params-table", tableInputs, switchParamsTable)
	return cases
}

// switchParamsTable is a raw-IR switch on an 8-bit selector over enough
// alternating cases for a jump table, passing the selector, a 64-bit and a
// 32-bit value on every edge.
func switchParamsTable() (*ir.SealedProgram, error) {
	i64, i32, i8 := ir.Scalar(64), ir.Scalar(32), ir.Scalar(8)
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	s, x, y := pb.Param(entry, i8), pb.Param(entry, i64), pb.Param(entry, i64)
	narrow := pb.Append(entry, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 32, A: y})
	blocks := make([]ir.Block, 4)
	for i := range blocks {
		blocks[i] = pb.NewBlock(i8, i64, i32)
	}
	list := make([]ir.Block, 12)
	for i := range list {
		list[i] = blocks[i%3]
	}
	pb.Switch(entry, s, list, blocks[3], s, x, narrow)
	for i, b := range blocks {
		sel := pb.Append(b, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: pb.BlockParam(b, 0)})
		wide := pb.Append(b, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: pb.BlockParam(b, 2)})
		k := pb.Append(b, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: uint64(2*i + 3)})
		m := pb.Append(b, ir.Instruction{Op: ir.OpMul, Bits: 64, A: pb.BlockParam(b, 1), B: k})
		pb.Return(b, pb.Append(b, ir.Instruction{Op: ir.OpAdd, Bits: 64, A: pb.Append(b, ir.Instruction{Op: ir.OpXor, Bits: 64, A: m, B: sel}), B: wide}))
	}
	return pb.Seal()
}

// switchInputs pairs selectors at and around the bounds of n cases, and
// beyond 32 bits, with a second argument.
func switchInputs(n int, w uint8) [][]uint64 {
	k := uint64(n)
	var inputs [][]uint64
	for _, s := range []uint64{^uint64(0), 0, 1, 2, k / 2, k - 1, k, k + 1, 1<<32 + 1, 1<<32 + k - 1, 1 << 63} {
		inputs = append(inputs, []uint64{s & mask(w), 0x0123456789abcdef})
	}
	return inputs
}

// switchBody switches on a w-bit selector over n cases. Target t returns
// the second argument mixed with t, and the default returns it plus 7, so
// every destination is told apart and the argument crosses blocks.
func switchBody(b *nyanjit.Builder, n int, pattern string, w uint8) {
	selector, x := b.ParamBits(w), b.Param()
	targets := n
	switch pattern {
	case "sparse":
		targets = max(1, (n+2)/3)
	case "same":
		targets = 1
	}
	labels := make([]nyanjit.Label, targets)
	for i := range labels {
		labels[i] = b.NewLabel()
	}
	otherwise := b.NewLabel()
	list := make([]nyanjit.Label, n)
	for i := range list {
		switch pattern {
		case "dense":
			list[i] = labels[i]
		case "sparse":
			// Runs, repeats and holes that fall to the default.
			if i%4 == 1 {
				list[i] = otherwise
			} else {
				list[i] = labels[(i*5/4)%targets]
			}
		default:
			list[i] = labels[0]
		}
	}
	b.Switch(selector, list, otherwise)
	for t, label := range labels {
		b.Bind(label)
		b.Return(b.Xor(b.Mul(x, b.Const(uint64(2*t+3))), b.Const(uint64(t))))
	}
	b.Bind(otherwise)
	b.Return(b.Add(x, b.Const(7)))
}

// switchLoop is threaded dispatch: every handler ends with its own Switch
// on the next opcode, taken from the accumulator, until the count runs out.
func switchLoop(b *nyanjit.Builder) {
	n, seed := b.Param(), b.Param()
	acc, count := b.Var(seed), b.Var(b.And(n, b.Const(63)))
	handlers := make([]nyanjit.Label, 8)
	for i := range handlers {
		handlers[i] = b.NewLabel()
	}
	other, exit := b.NewLabel(), b.NewLabel()
	list := make([]nyanjit.Label, 12)
	for i := range list {
		list[i] = handlers[i%8]
	}
	dispatch := func() {
		b.Branch(b.IsZero(b.Get(count)), exit)
		b.Set(count, b.Sub(b.Get(count), b.Const(1)))
		b.Switch(b.And(b.Get(acc), b.Const(15)), list, other)
	}
	dispatch()
	for i, h := range handlers {
		b.Bind(h)
		a := b.Get(acc)
		b.Set(acc, b.Add(b.Mul(a, b.Const(uint64(2*i+5))), b.ShiftRight(a, uint8(i+1))))
		dispatch()
	}
	b.Bind(other)
	b.Set(acc, b.Xor(b.Get(acc), b.Const(0x5bd1e995)))
	dispatch()
	b.Bind(exit)
	b.Return(b.Get(acc))
}

// switchParams passes block parameters on every edge of a raw-IR switch with
// duplicate cases, a 64-bit selector and targets that read the parameters
// in different orders.
func switchParams() (*ir.SealedProgram, error) {
	i64 := ir.Scalar(64)
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	s, x, y := pb.Param(entry, i64), pb.Param(entry, i64), pb.Param(entry, i64)
	sum := pb.Append(entry, ir.Instruction{Op: ir.OpAdd, Bits: 64, A: x, B: y})
	blocks := make([]ir.Block, 4)
	for i := range blocks {
		blocks[i] = pb.NewBlock(i64, i64, i64)
	}
	pb.Switch(entry, s, []ir.Block{blocks[1], blocks[0], blocks[1], blocks[2]}, blocks[3], sum, x, y)
	for i, b := range blocks {
		p, q, r := pb.BlockParam(b, 0), pb.BlockParam(b, 1), pb.BlockParam(b, 2)
		k := pb.Append(b, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: uint64(i*i + 3)})
		m := pb.Append(b, ir.Instruction{Op: ir.OpMul, Bits: 64, A: []int{p, q, r, p}[i], B: k})
		pb.Return(b, pb.Append(b, ir.Instruction{Op: ir.OpSub, Bits: 64, A: m, B: []int{r, p, q, q}[i]}))
	}
	return pb.Seal()
}

// switchParamsLoop carries a counter and an accumulator through a raw-IR
// loop whose body switches on a 32-bit selector, every case passing both.
func switchParamsLoop() (*ir.SealedProgram, error) {
	i64, i32 := ir.Scalar(64), ir.Scalar(32)
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	k, seed := pb.Param(entry, i64), pb.Param(entry, i64)
	constant := func(b ir.Block, bits uint8, x uint64) int {
		return pb.Append(b, ir.Instruction{Op: ir.OpConst, Bits: bits, Imm: x})
	}
	n := pb.Append(entry, ir.Instruction{Op: ir.OpAnd, Bits: 64, A: k, B: constant(entry, 64, 15)})
	head, exit := pb.NewBlock(i64, i64), pb.NewBlock(i64)
	pb.Jump(entry, head, constant(entry, 64, 0), seed)
	i, acc := pb.BlockParam(head, 0), pb.BlockParam(head, 1)
	body := pb.NewBlock()
	pb.Branch(head, compareValues(ir.BranchBelow, i, n), body, nil, exit, []int{acc})
	low := pb.Append(body, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 32, A: acc})
	selector := pb.Append(body, ir.Instruction{Op: ir.OpAnd, Bits: 32, A: low, B: constant(body, 32, 7)})
	arms := make([]ir.Block, 3)
	for a := range arms {
		arms[a] = pb.NewBlock(i64, i32)
	}
	next := pb.Append(body, ir.Instruction{Op: ir.OpAdd, Bits: 64, A: i, B: constant(body, 64, 1)})
	pb.Switch(body, selector, []ir.Block{arms[0], arms[1], arms[0], arms[2], arms[1]}, arms[2], next, selector)
	for a, arm := range arms {
		j, sel := pb.BlockParam(arm, 0), pb.BlockParam(arm, 1)
		wide := pb.Append(arm, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: sel})
		m := pb.Append(arm, ir.Instruction{Op: ir.OpMul, Bits: 64, A: acc, B: constant(arm, 64, uint64(2*a+3))})
		pb.Jump(arm, head, j, pb.Append(arm, ir.Instruction{Op: ir.OpAdd, Bits: 64, A: m, B: wide}))
	}
	pb.Return(exit, pb.BlockParam(exit, 0))
	return pb.Seal()
}
