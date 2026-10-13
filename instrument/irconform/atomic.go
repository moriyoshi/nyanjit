package irconform

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

var atomicOperations = []ir.Opcode{ir.OpAtomicLoad, ir.OpAtomicStore, ir.OpAtomicAdd, ir.OpAtomicAnd, ir.OpAtomicOr, ir.OpAtomicXor, ir.OpAtomicSwap, ir.OpAtomicCompareSwap}

// patternValue is the w-bit little-endian value Check's buffer pattern holds
// at offset.
func patternValue(offset uint64, w uint8) uint64 {
	var x uint64
	for k := uint64(0); k < uint64(w/8); k++ {
		x |= uint64(byte((offset+k)*37+11)) << (8 * k)
	}
	return x
}

// atomicCases are the memory/atomic-<op>-<w> cases: each operation at each
// width at aligned offsets of the buffer, returning the old value, with the
// buffer compared afterwards. Compare-and-swap inputs include the value in
// memory, so it both succeeds and fails. Further cases discard the result,
// which must not remove the operation, and order plain accesses with Fence.
func atomicCases() []Case {
	var cases []Case
	for _, w := range widths {
		size := uint64(w / 8)
		for _, operation := range atomicOperations {
			offsets := []uint64{0, size, 8, 24, memorySize - size}
			if size == 8 {
				offsets = offsets[1:] // 0 and 8 once each
				offsets[0] = 0
			}
			for _, offset := range offsets {
				current := patternValue(offset, w)
				inputs := [][]uint64{{0, 0}, {1, mask(w)}, {mask(w), 1}, {0x0123456789abcdef & mask(w), 0x7e & mask(w)},
					{current, ^current & mask(w)}, {current ^ 1, 5}}
				body := []ir.Instruction{
					{Op: ir.OpConst, Bits: nativeBits, Imm: offset},
					{Op: ir.OpAdd, Bits: nativeBits, A: 0, B: 3},
					atomicInstruction(operation, w, 4, 1, 2),
				}
				if operation == ir.OpAtomicStore {
					body = append(body, ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 0})
				}
				cases = append(cases, Case{Name: name("memory", "atomic", operation, w, offset), Buffer: memorySize, Inputs: inputs,
					Build: straight(append(params(nativeBits, w, w), body...)...)})
			}
			if operation == ir.OpAtomicLoad || operation == ir.OpAtomicStore {
				continue
			}
			// The result is unused: the store still happens.
			cases = append(cases, Case{Name: name("memory", "atomic", operation, w, "unused"), Buffer: memorySize,
				Inputs: [][]uint64{{1, 2}, {patternValue(16, w), mask(w)}},
				Build: straight(append(params(nativeBits, w, w),
					ir.Instruction{Op: ir.OpConst, Bits: nativeBits, Imm: 16},
					ir.Instruction{Op: ir.OpAdd, Bits: nativeBits, A: 0, B: 3},
					atomicInstruction(operation, w, 4, 1, 2),
					ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 0})...)})
		}
	}
	// Plain accesses around a fence keep their order.
	cases = append(cases, Case{Name: "memory/fence", Buffer: memorySize, Inputs: [][]uint64{{0}, {0x0123456789abcdef}},
		Build: straight(append(params(nativeBits, 64),
			ir.Instruction{Op: ir.OpStore64, A: 0, B: 1},
			ir.Instruction{Op: ir.OpFence},
			ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 0})...)})
	// A compare-and-swap retry loop in control flow: it adds the input to a
	// word n times through AtomicLoad and AtomicCompareSwap.
	cases = append(cases, Case{Name: "control/atomic-cas-loop", Buffer: memorySize, Inputs: [][]uint64{{0, 3}, {5, 1}, {^uint64(0), 4}},
		Build: built(func(b *nyanjit.Builder) {
			buffer, x, count := b.ParamBits(nativeBits), b.Param(), b.Param()
			address := b.Add(buffer, b.ConstBits(nativeBits, 8))
			i := b.Var(b.Const(0))
			loop, retry, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
			b.Bind(loop)
			b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, count), done)
			b.Bind(retry)
			old := b.AtomicLoad(address, 64)
			b.Branch(b.Compare(b.AtomicCompareSwap(address, old, b.Add(old, x)), ir.BranchNotEqual, old), retry)
			b.Set(i, b.Add(b.Get(i), b.Const(1)))
			b.Jump(loop)
			b.Bind(done)
			b.Return(b.AtomicAdd(address, b.Const(0)))
		})})
	return cases
}

// atomicInstruction is operation at width w on address a with operand b and,
// for compare-and-swap, replacement c.
func atomicInstruction(operation ir.Opcode, w uint8, a, b, c int) ir.Instruction {
	ins := ir.Instruction{Op: operation, Bits: w, A: a}
	switch operation {
	case ir.OpAtomicLoad:
	case ir.OpAtomicCompareSwap:
		ins.B, ins.C = b, c
	default:
		ins.B = b
	}
	return ins
}
