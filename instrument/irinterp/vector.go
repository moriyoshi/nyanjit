package irinterp

import (
	"encoding/binary"
	"fmt"

	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

// vector executes a vector or mask operation lane by lane, with the meaning
// that scalar lowering (VectorsOff) gives it.
func (m *machine) vector(i int, ins *ir.Instruction) error {
	t := ins.ValueType()
	lanes := int(t.Lanes)
	bits := t.LaneBits
	mask := widthMask(bits)
	vals := m.vals
	out := make([]uint64, lanes)
	set := func(inputs ...value) { vals[i] = value{lanes: out, t: merge(inputs...)} }
	switch ins.Op {
	case ir.OpVZero:
		set()
	case ir.OpVConst:
		size := int(bits / 8)
		for n := range out {
			var word [8]byte
			copy(word[:], ins.Data[n*size:(n+1)*size])
			out[n] = binary.LittleEndian.Uint64(word[:]) & mask
		}
		set()
	case ir.OpVSplat:
		a := vals[ins.A]
		for n := range out {
			out[n] = a.x & mask
		}
		set(a)
	case ir.OpVLoad:
		a := vals[ins.A]
		size := int(bits / 8)
		for n := range out {
			out[n] = load(a.x+uint64(n*size), size, ins.ByteOrder)
		}
		set(a)
		if m.memory {
			vals[i].t = unknown
		}
	case ir.OpVStore:
		a, b := vals[ins.A], vals[ins.B]
		if a.t != exact || b.t != exact {
			m.memory = true
		}
		size := int(bits / 8)
		for n, x := range b.lanes {
			store(a.x+uint64(n*size), size, ins.ByteOrder, x)
		}
	case ir.OpVExtract:
		a := vals[ins.A]
		vals[i] = value{x: a.lanes[ins.Imm], t: merge(a)}
	case ir.OpVInsert, ir.OpVInsertZero:
		a, b := vals[ins.A], vals[ins.B]
		copy(out, a.lanes)
		out[ins.Imm] = b.x & mask
		set(a, b)
	case ir.OpVCompare:
		a, b := vals[ins.A], vals[ins.B]
		for n := range out {
			if compare(ir.BranchCondition(ins.Imm), a.lanes[n], b.lanes[n], ins.Bits) {
				out[n] = 1
			}
		}
		set(a, b)
	case ir.OpVMaskBits:
		a := vals[ins.A]
		var x uint64
		for n, lane := range a.lanes {
			x |= (lane & 1) << n
		}
		vals[i] = value{x: x & widthMask(ins.Bits), t: merge(a)}
	case ir.OpVCarrylessMul:
		a, b := vals[ins.A], vals[ins.B]
		for chunk := 0; chunk < lanes; chunk += 2 {
			x, y := a.lanes[chunk+int(ins.Imm&1)], b.lanes[chunk+int(ins.Imm>>1)]
			var lo, hi uint64
			for bit := range uint64(64) {
				if y>>bit&1 == 0 {
					continue
				}
				lo ^= x << bit
				if bit != 0 {
					hi ^= x >> (64 - bit)
				}
			}
			out[chunk], out[chunk+1] = lo, hi
		}
		set(a, b)
	case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
		a, b := vals[ins.A], vals[ins.B]
		for n := range out {
			out[n] = integer(ins.Op, a.lanes[n], b.lanes[n], bits)
		}
		set(a, b)
	case ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
		a := vals[ins.A]
		for n := range out {
			out[n] = shift(ins.Op, a.lanes[n], ins.Imm, bits)
		}
		set(a)
	case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
		immediate, _ := op.ShiftByOperation(ins.Op)
		a, b := vals[ins.A], vals[ins.B]
		for n := range out {
			count := b.x
			if b.lanes != nil {
				count = b.lanes[n]
			}
			out[n] = shift(immediate, a.lanes[n], count, bits)
		}
		set(a, b)
	default:
		return fmt.Errorf("vector %s at value %d: %w", ins.Op, i, ErrUnsupported)
	}
	return nil
}
