package machine

import (
	"github.com/moriyoshi/nyanjit/ir"
	"math"
)

// appendProductError selects the error of a well-scaled binary64 product.
// The rounded product is already available as p. Keep every Dekker operation
// separately rounded; polynomial contraction must not change this identity.
func appendProductError(program *SelectedProgram, dst, a, b, p int, fma bool) {
	value := func() int {
		id := len(program.Values)
		program.Values = append(program.Values, SelectedValue{Words: 1, Param: -1, EntrySlot: -1, EntryReg: -1})
		return id
	}
	constant := func(bits uint64) int {
		id := value()
		program.Values[id].Constant = true
		program.Values[id].Imm = bits
		program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedConst, Bits: 64, Dst: id, Imm: bits})
		return id
	}
	binary := func(op ir.Opcode, a, b int) int {
		id := value()
		program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedFloatBinary, Arithmetic: op, Bits: 64, Dst: id, A: a, B: b})
		return id
	}
	if fma {
		sign := constant(uint64(1) << 63)
		neg := value()
		program.Instructions = append(program.Instructions, SelectedInstruction{Op: SelectedBinary, Arithmetic: ir.OpXor, Bits: 64, Dst: neg, A: p, B: sign}, SelectedInstruction{Op: SelectedFloatFMA, Bits: 64, Dst: dst, A: a, B: b, C: neg})
		return
	}
	split := func(a int) (int, int) {
		t := binary(ir.OpFMul, constant(math.Float64bits(134217729)), a)
		hi := binary(ir.OpFSub, t, binary(ir.OpFSub, t, a))
		return hi, binary(ir.OpFSub, a, hi)
	}
	ah, al := split(a)
	bh, bl := split(b)
	e := binary(ir.OpFAdd, binary(ir.OpFAdd, binary(ir.OpFAdd, binary(ir.OpFSub, binary(ir.OpFMul, ah, bh), p), binary(ir.OpFMul, ah, bl)), binary(ir.OpFMul, al, bh)), binary(ir.OpFMul, al, bl))
	_ = e
	program.Instructions[len(program.Instructions)-1].Dst = dst
}

func verifyProductError(a, b, p uint64, fused bool) uint64 {
	x, y, z := math.Float64frombits(a), math.Float64frombits(b), math.Float64frombits(p)
	if fused {
		return math.Float64bits(math.FMA(x, y, -z))
	}
	split := func(a float64) (float64, float64) {
		t := float64(134217729 * a)
		hi := float64(t - float64(t-a))
		return hi, float64(a - hi)
	}
	ah, al := split(x)
	bh, bl := split(y)
	return math.Float64bits(float64(float64(float64(float64(ah*bh)-z)+float64(ah*bl))+float64(al*bh)) + float64(al*bl))
}
