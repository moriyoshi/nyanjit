package irconform

import "github.com/moriyoshi/nyanjit/ir"

// vectorSize holds two source vectors of up to 256 bits at offsets 0 and 32
// and the destination at offset 64.
const vectorSize = 128

var vectorTypes = []ir.Type{ir.Vec(16, 8), ir.Vec(8, 16), ir.Vec(4, 32), ir.Vec(2, 64), ir.Vec(8, 32), ir.Vec(4, 64)}

// vectorProgram loads x from buffer+0 and y from buffer+32, applies body, and
// stores the vector it returns to buffer+64, then returns scalar (or zero).
// body receives the next free value index and returns its instructions, the
// stored vector and the returned scalar (or -1).
func vectorProgram(t ir.Type, extra []uint8, body func(next, x, y int) ([]ir.Instruction, int, int)) func() (*ir.SealedProgram, error) {
	return func() (*ir.SealedProgram, error) {
		ins := params(append([]uint8{nativeBits}, extra...)...)
		at := func(offset uint64) int {
			ins = append(ins, ir.Instruction{Op: ir.OpConst, Bits: nativeBits, Imm: offset},
				ir.Instruction{Op: ir.OpAdd, Bits: nativeBits, A: 0, B: len(ins)})
			return len(ins) - 1
		}
		load := func(offset uint64) int {
			address := at(offset)
			ins = append(ins, ir.Instruction{Op: ir.OpVLoad, Type: t, Bits: t.LaneBits, A: address})
			return len(ins) - 1
		}
		x, y := load(0), load(32)
		destination := at(64)
		more, stored, scalar := body(len(ins), x, y)
		ins = append(ins, more...)
		if stored >= 0 {
			typ := ins[stored].ValueType()
			ins = append(ins, ir.Instruction{Op: ir.OpVStore, Type: typ, Bits: typ.LaneBits, A: destination, B: stored})
		}
		if scalar < 0 {
			ins = append(ins, ir.Instruction{Op: ir.OpConst, Bits: 64})
			scalar = len(ins) - 1
		}
		arity := 1 + len(extra)
		return ir.Seal(&ir.Program{Instructions: ins, Arity: arity, Result: scalar})
	}
}

func vectorCases() []Case {
	var cases []Case
	none := [][]uint64{{}}
	add := func(group string, t ir.Type, inputs [][]uint64, extra []uint8, body func(next, x, y int) ([]ir.Instruction, int, int), detail ...any) {
		cases = append(cases, Case{Name: name("vector", append([]any{group, t.Lanes, t.LaneBits}, detail...)...), Buffer: vectorSize, Inputs: inputs, Build: vectorProgram(t, extra, body)})
	}
	for _, t := range vectorTypes {
		bits := t.LaneBits
		for _, op := range []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor} {
			add(op.String(), t, none, nil, func(next, x, y int) ([]ir.Instruction, int, int) {
				return []ir.Instruction{{Op: op, Type: t, Bits: bits, A: x, B: y}}, next, -1
			})
		}
		for _, op := range []ir.Opcode{ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight} {
			for _, count := range []uint64{0, 1, uint64(bits) - 1, uint64(bits), uint64(bits) + 3} {
				add(op.String(), t, none, nil, func(next, x, y int) ([]ir.Instruction, int, int) {
					return []ir.Instruction{{Op: op, Type: t, Bits: bits, A: x, Imm: count}}, next, -1
				}, count)
			}
		}
		for _, op := range []ir.Opcode{ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy} {
			var inputs [][]uint64
			for _, n := range counts(bits) {
				inputs = append(inputs, []uint64{n})
			}
			add(op.String(), t, inputs, []uint8{64}, func(next, x, y int) ([]ir.Instruction, int, int) {
				return []ir.Instruction{{Op: op, Type: t, Bits: bits, A: x, B: 1}}, next, -1
			}, "scalar")
			add(op.String(), t, none, nil, func(next, x, y int) ([]ir.Instruction, int, int) {
				return []ir.Instruction{{Op: op, Type: t, Bits: bits, A: x, B: y}}, next, -1
			}, "vector")
		}
		for condition := ir.BranchEqual; condition <= ir.BranchAbove; condition++ {
			add("OpVCompare", t, none, nil, func(next, x, y int) ([]ir.Instruction, int, int) {
				maskBits := uint8(8)
				for uint16(maskBits) < t.Lanes {
					maskBits *= 2
				}
				return []ir.Instruction{
					{Op: ir.OpVCompare, Type: ir.Mask(t.Lanes), Bits: bits, A: x, B: y, Imm: uint64(condition)},
					{Op: ir.OpVMaskBits, Bits: maskBits, A: next},
				}, -1, next + 1
			}, conditionName[condition])
			// Equal lanes exercise the non-strict conditions.
			add("OpVCompare", t, none, nil, func(next, x, y int) ([]ir.Instruction, int, int) {
				maskBits := uint8(8)
				for uint16(maskBits) < t.Lanes {
					maskBits *= 2
				}
				return []ir.Instruction{
					{Op: ir.OpVCompare, Type: ir.Mask(t.Lanes), Bits: bits, A: x, B: x, Imm: uint64(condition)},
					{Op: ir.OpVMaskBits, Bits: maskBits, A: next},
				}, -1, next + 1
			}, conditionName[condition], "same")
		}
		scalars := [][]uint64{{0}, {1}, {mask(bits)}, {0x0123456789abcdef & mask(bits)}}
		add("OpVSplat", t, scalars, []uint8{bits}, func(next, x, y int) ([]ir.Instruction, int, int) {
			return []ir.Instruction{{Op: ir.OpVSplat, Type: t, Bits: bits, A: 1}, {Op: ir.OpXor, Type: t, Bits: bits, A: next, B: y}}, next + 1, -1
		})
		data := make([]byte, t.Bits()/8)
		for i := range data {
			data[i] = byte(0xf1 - i*13)
		}
		add("OpVConst", t, none, nil, func(next, x, y int) ([]ir.Instruction, int, int) {
			return []ir.Instruction{{Op: ir.OpVConst, Type: t, Bits: bits, Data: data}, {Op: ir.OpAdd, Type: t, Bits: bits, A: next, B: x}}, next + 1, -1
		})
		add("OpVZero", t, none, nil, func(next, x, y int) ([]ir.Instruction, int, int) {
			return []ir.Instruction{{Op: ir.OpVZero, Type: t, Bits: bits}, {Op: ir.OpOr, Type: t, Bits: bits, A: next, B: y}}, next + 1, -1
		})
		for _, lane := range []uint64{0, 1, uint64(t.Lanes) - 1}[:min(3, t.Lanes)] {
			add("OpVExtract", t, none, nil, func(next, x, y int) ([]ir.Instruction, int, int) {
				return []ir.Instruction{{Op: ir.OpVExtract, Bits: bits, Type: ir.Scalar(bits), A: y, Imm: lane}}, -1, next
			}, lane)
			for _, op := range []ir.Opcode{ir.OpVInsert, ir.OpVInsertZero} {
				add(op.String(), t, scalars, []uint8{bits}, func(next, x, y int) ([]ir.Instruction, int, int) {
					return []ir.Instruction{{Op: op, Type: t, Bits: bits, A: x, B: 1, Imm: lane}}, next, -1
				}, lane)
			}
		}
		if bits == 64 {
			for imm := uint64(0); imm < 4; imm++ {
				add("OpVCarrylessMul", t, none, nil, func(next, x, y int) ([]ir.Instruction, int, int) {
					return []ir.Instruction{{Op: ir.OpVCarrylessMul, Type: t, Bits: 64, A: x, B: y, Imm: imm}}, next, -1
				}, imm)
			}
		}
	}
	return cases
}
