package machine

import (
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/regalloc"
)

// ErrLoweringMismatch means instruction selection changed IR semantics.
var ErrLoweringMismatch = errors.New("nyanjit: lowered instructions do not match the builder")

// verifySelectedProgram interprets both sides of instruction selection with
// deterministic scalar inputs and a byte-addressed native-memory model. It is
// a compile-time guard against incorrect rewrites. Fragments with calls or
// syscalls are skipped because their external effects are not modeled here.
func verifySelectedProgram(source []ir.Instruction, selected SelectedProgram, result, maxArgs int, registerBits uint8, nativeOrder ir.ByteOrder) error {
	for _, ins := range source {
		if ins.Op == ir.OpGet || ins.Op == ir.OpSet || ins.Op == ir.OpScopeEnter || ins.Op == ir.OpScopeLeave {
			return nil
		}
		if ins.IsVector() {
			return nil
		} // native SIMD is checked by lane-reference differential execution
		if ins.Op == ir.OpSyscall || ins.Op == ir.OpCallC || ins.Op == ir.OpCallGo {
			return nil
		}
	}
	fusedAdds := make(map[int]SelectedInstruction)
	fusedProducts := make(map[int]bool)
	for _, ins := range selected.Instructions {
		if ins.Op == SelectedFloatFMA && ins.Dst >= 0 && ins.Dst < len(source) && source[ins.Dst].Op == ir.OpFProductError {
			fusedProducts[ins.Dst] = true
		}
		if ins.Op == SelectedFloatFMA && ins.Dst >= 0 && ins.Dst < len(source) && source[ins.Dst].Op == ir.OpFAdd {
			fusedAdds[ins.Dst] = ins
		}
	}
	for sample := uint64(1); sample <= 5; sample++ {
		inputs := make([]uint64, maxArgs)
		for i := range inputs {
			inputs[i] = mixVerify(sample*0x9e3779b97f4a7c15 + uint64(i+1))
		}
		initial := sample * 0x517cc1b727220a95
		originalMemory := verifyMemory{seed: initial, written: make(map[uint64]byte), nativeOrder: nativeOrder}
		selectedMemory := verifyMemory{seed: initial, written: make(map[uint64]byte), nativeOrder: nativeOrder}
		original := make([]uint64, len(source))
		param := 0
		for id, ins := range source {
			switch ins.Op {
			case ir.OpParam:
				original[id] = inputs[param] & verifyIntegerMask(selectedInstructionBits(ins))
				param++
			case ir.OpConst:
				original[id] = ins.Imm
				if ins.Symbol != "" {
					original[id] = verifySymbol(ins.Symbol, initial)
				}
			case ir.OpLabelParam:
				original[id] = verifyLabelInput(ins.EntryReg, ins.Imm, initial)
			case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor:
				original[id] = verifyBinary(ins.Op, original[ins.A], original[ins.B]) & verifyIntegerMask(selectedInstructionBits(ins))
			case ir.OpCarryAdd, ir.OpBorrowSub, ir.OpAddWithCarry, ir.OpSubWithBorrow:
				carry := uint64(0)
				if ins.Op == ir.OpAddWithCarry || ins.Op == ir.OpSubWithBorrow {
					carry = original[ins.C]
				}
				original[id] = verifyWordCarry(ins.Op, selectedInstructionBits(ins), original[ins.A], original[ins.B], carry)
			case ir.OpLeadingZeros, ir.OpTrailingZeros:
				original[id] = verifyCountZeros(ins.Op, selectedInstructionBits(ins), original[ins.A])
			case ir.OpMulHigh:
				original[id] = verifyMulHigh(selectedInstructionBits(ins), original[ins.A], original[ins.B])
			case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv:
				if fused, ok := fusedAdds[id]; ok {
					original[id] = verifyFMA(ins.Bits, original[fused.A], original[fused.B], original[fused.C])
				} else {
					original[id] = verifyFloat(ins.Op, ins.Bits, original[ins.A], original[ins.B])
				}
			case ir.OpFProductError:
				original[id] = verifyProductError(original[ins.A], original[ins.B], original[ins.C], fusedProducts[id])
			case ir.OpFMA:
				original[id] = verifyFMA(ins.Bits, original[ins.A], original[ins.B], original[ins.C])
			case ir.OpFSqrt, ir.OpFConvert:
				original[id] = verifyFloatUnary(ins.Op, ins.Bits, original[ins.A])
			case ir.OpCastUnsigned:
				original[id] = original[ins.A] & verifyIntegerMask(selectedInstructionBits(ins))
			case ir.OpSignExtend:
				width := uint8(ins.Imm)
				low := original[ins.A] & verifyIntegerMask(width)
				original[id] = uint64(int64(low<<(64-width)) >> (64 - width))
			case ir.OpAddScaled:
				original[id] = (original[ins.A] + original[ins.B]*ins.Imm) & verifyIntegerMask(selectedInstructionBits(ins))
			case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
				operation, _ := ir.ShiftByOperation(ins.Op)
				original[id] = verifyShiftBy(operation, selectedInstructionBits(ins), original[ins.A], original[ins.B])
			case ir.OpShiftRight, ir.OpShiftLeft, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
				original[id] = verifyShift(ins.Op, selectedInstructionBits(ins), original[ins.A], uint8(ins.Imm))
			case ir.OpSelect:
				if original[ins.A] != 0 {
					original[id] = original[ins.B]
				} else {
					original[id] = original[ins.C]
				}
			case ir.OpLoad8:
				original[id] = uint64(originalMemory.load8(original[ins.A]))
			case ir.OpLoad64:
				original[id] = originalMemory.load64(original[ins.A], ins.ByteOrder)
			case ir.OpStore8:
				originalMemory.store8(original[ins.A], byte(original[ins.B]))
			case ir.OpStore64:
				originalMemory.store64(original[ins.A], original[ins.B], ins.ByteOrder)
			default:
				return regalloc.ErrInvalidFunction
			}
		}
		lowered := make([]uint64, len(selected.Values))
		for id, value := range selected.Values {
			if value.Param >= 0 {
				lowered[id] = inputs[value.Param]
			} else if value.EntrySlot >= 0 {
				lowered[id] = verifyLabelInput(-1, uint64(value.EntrySlot), initial)
			} else if value.EntryReg >= 0 {
				lowered[id] = verifyLabelInput(value.EntryReg, 0, initial)
			}
		}
		for _, ins := range selected.Instructions {
			switch ins.Op {
			case SelectedConst:
				lowered[ins.Dst] = ins.Imm
			case SelectedSymbol:
				lowered[ins.Dst] = verifySymbol(ins.Symbol, initial)
			case SelectedMove:
				lowered[ins.Dst] = lowered[ins.A]
			case SelectedBinary:
				width := ins.Bits
				if width == 0 {
					width = uint8(selected.Values[ins.Dst].Words) * registerBits
				}
				lowered[ins.Dst] = verifyBinary(ins.Arithmetic, lowered[ins.A], lowered[ins.B]) & verifyIntegerMask(width)
			case SelectedFloatBinary:
				lowered[ins.Dst] = verifyFloat(ins.Arithmetic, ins.Bits, lowered[ins.A], lowered[ins.B])
			case SelectedFloatFMA:
				lowered[ins.Dst] = verifyFMA(ins.Bits, lowered[ins.A], lowered[ins.B], lowered[ins.C])
			case SelectedAndNot:
				lowered[ins.Dst] = (lowered[ins.A] &^ lowered[ins.B]) & verifyIntegerMask(ins.Bits)
			case SelectedCountZeros:
				lowered[ins.Dst] = verifyCountZeros(ins.Arithmetic, ins.Bits, lowered[ins.A])
			case SelectedFloatUnary:
				lowered[ins.Dst] = verifyFloatUnary(ins.Arithmetic, ins.Bits, lowered[ins.A])
			case SelectedBinaryImmediate:
				lowered[ins.Dst] = verifyBinary(ins.Arithmetic, lowered[ins.A], ins.Imm)
				if ins.Bits == registerBits || ins.ZeroExtended {
					lowered[ins.Dst] &= verifyIntegerMask(ins.Bits)
				}
			case SelectedDec:
				lowered[ins.Dst] = (lowered[ins.A] - 1) & verifyIntegerMask(registerBits)
			case SelectedInc:
				lowered[ins.Dst] = (lowered[ins.A] + 1) & verifyIntegerMask(registerBits)
			case SelectedNarrow:
				lowered[ins.Dst] = lowered[ins.A] & verifyIntegerMask(uint8(ins.Imm))
			case SelectedSignExtend:
				width := uint8(ins.Imm)
				low := lowered[ins.A] & verifyIntegerMask(width)
				lowered[ins.Dst] = uint64(int64(low<<(64-width)) >> (64 - width))
			case SelectedCarryBit:
				op := ir.OpCarryAdd
				if ins.Arithmetic == ir.OpSub {
					op = ir.OpBorrowSub
				}
				lowered[ins.Dst] = verifyWordCarry(op, ins.Bits, lowered[ins.A], lowered[ins.B], 0)
			case SelectedWordCarry:
				op, carryOp := ir.OpAddWithCarry, ir.OpCarryAdd
				if ins.Arithmetic == ir.OpSub {
					op, carryOp = ir.OpSubWithBorrow, ir.OpBorrowSub
				}
				carry := uint64(0)
				if len(ins.Args) != 0 {
					carry = verifyWordCarry(carryOp, ins.Bits, lowered[ins.Args[2]], lowered[ins.Args[3]], 0)
				} else {
					carry = lowered[ins.C]
				}
				lowered[ins.Dst] = verifyWordCarry(op, ins.Bits, lowered[ins.A], lowered[ins.B], carry)
			case SelectedMulHigh:
				lowered[ins.Dst] = verifyMulHigh(ins.Bits, lowered[ins.A], lowered[ins.B])
			case SelectedShiftBy:
				lowered[ins.Dst] = verifyShiftBy(ins.Arithmetic, ins.Bits, lowered[ins.A], lowered[ins.B])
			case SelectedShift:
				lowered[ins.Dst] = verifyShift(ins.Arithmetic, ins.Bits, lowered[ins.A], uint8(ins.Imm))
			case SelectedScaled:
				lowered[ins.Dst] = (lowered[ins.A] + lowered[ins.B]*ins.Imm + ins.Disp) & verifyIntegerMask(uint8(selected.Values[ins.Dst].Words)*registerBits)
			case SelectedMulAdd:
				lowered[ins.Dst] = lowered[ins.A] + lowered[ins.B]*lowered[ins.C]
			case SelectedSelect:
				if lowered[ins.A] != 0 {
					lowered[ins.Dst] = lowered[ins.B]
				} else {
					lowered[ins.Dst] = lowered[ins.C]
				}
			case SelectedLoad8:
				lowered[ins.Dst] = uint64(selectedMemory.load8(lowered[ins.A]))
			case SelectedLoad64:
				lowered[ins.Dst] = selectedMemory.load64(lowered[ins.A], ins.ByteOrder)
			case SelectedLoadAddress:
				lowered[ins.Dst] = selectedMemory.load64(lowered[ins.A]+lowered[ins.B]*ins.Imm+ins.Disp, ins.ByteOrder)
			case SelectedLoadOffset:
				lowered[ins.Dst] = selectedMemory.load64(lowered[ins.A]+ins.Disp, ins.ByteOrder)
			case SelectedStore8:
				selectedMemory.store8(lowered[ins.A], byte(lowered[ins.B]))
			case SelectedStore64:
				selectedMemory.store64(lowered[ins.A], lowered[ins.B], ins.ByteOrder)
			case SelectedStoreAddress:
				selectedMemory.store64(lowered[ins.A]+lowered[ins.B]*ins.Imm+ins.Disp, lowered[ins.C], ins.ByteOrder)
			case SelectedStoreOffset:
				selectedMemory.store64(lowered[ins.A]+ins.Disp, lowered[ins.B], ins.ByteOrder)
			default:
				return regalloc.ErrInvalidFunction
			}
		}
		if original[result] != lowered[selected.Result] {
			return fmt.Errorf("%w: sample %d result %d: IR %#x, selected %#x", ErrLoweringMismatch, sample, result, original[result], lowered[selected.Result])
		}
		if !sameVerifyMemory(originalMemory, selectedMemory) {
			return fmt.Errorf("%w: sample %d memory effects differ", ErrLoweringMismatch, sample)
		}
		for _, id := range selected.Exports {
			if original[id] != lowered[id] {
				return fmt.Errorf("%w: sample %d export %d: IR %#x, selected %#x", ErrLoweringMismatch, sample, id, original[id], lowered[id])
			}
		}
	}
	return nil
}

func verifyShift(operation ir.Opcode, width uint8, value uint64, count uint8) uint64 {
	mask := verifyIntegerMask(width)
	value &= mask
	switch operation {
	case ir.OpShiftLeft:
		return (value << count) & mask
	case ir.OpShiftRight:
		return value >> count
	case ir.OpShiftRightArithmetic:
		signed := int64(value<<(64-width)) >> (64 - width)
		return uint64(signed>>count) & mask
	case ir.OpRotateLeft:
		return (value<<count | value>>(width-count)) & mask
	case ir.OpRotateRight:
		return (value>>count | value<<(width-count)) & mask
	default:
		panic("nyanjit: verifier received invalid shift operation")
	}
}

func verifyBinary(operation ir.Opcode, left, right uint64) uint64 {
	switch operation {
	case ir.OpAdd:
		return left + right
	case ir.OpSub:
		return left - right
	case ir.OpMul:
		return left * right
	case ir.OpAnd:
		return left & right
	case ir.OpOr:
		return left | right
	case ir.OpXor:
		return left ^ right
	default:
		panic("nyanjit: verifier received invalid arithmetic operation")
	}
}

func verifyFloatUnary(operation ir.Opcode, width uint8, input uint64) uint64 {
	if operation == ir.OpFConvert {
		if width == 32 {
			return uint64(math.Float32bits(float32(math.Float64frombits(input))))
		}
		return math.Float64bits(float64(math.Float32frombits(uint32(input))))
	}
	if operation != ir.OpFSqrt {
		panic("nyanjit: verifier received invalid floating unary operation")
	}
	if width == 32 {
		return uint64(math.Float32bits(float32(math.Sqrt(float64(math.Float32frombits(uint32(input)))))))
	}
	return math.Float64bits(math.Sqrt(math.Float64frombits(input)))
}

func verifyFloat(operation ir.Opcode, width uint8, left, right uint64) uint64 {
	if width == 32 {
		a, b := math.Float32frombits(uint32(left)), math.Float32frombits(uint32(right))
		switch operation {
		case ir.OpFAdd:
			return uint64(math.Float32bits(a + b))
		case ir.OpFSub:
			return uint64(math.Float32bits(a - b))
		case ir.OpFMul:
			return uint64(math.Float32bits(a * b))
		case ir.OpFDiv:
			return uint64(math.Float32bits(a / b))
		}
	}
	a, b := math.Float64frombits(left), math.Float64frombits(right)
	switch operation {
	case ir.OpFAdd:
		return math.Float64bits(a + b)
	case ir.OpFSub:
		return math.Float64bits(a - b)
	case ir.OpFMul:
		return math.Float64bits(a * b)
	case ir.OpFDiv:
		return math.Float64bits(a / b)
	}
	panic("nyanjit: verifier received invalid floating operation")
}

func verifyFMA(width uint8, x, y, addend uint64) uint64 {
	if width == 32 {
		a := float64(math.Float32frombits(uint32(x)))
		b := float64(math.Float32frombits(uint32(y)))
		c := float64(math.Float32frombits(uint32(addend)))
		if math.IsNaN(a) || math.IsNaN(b) || math.IsNaN(c) ||
			math.IsInf(a, 0) || math.IsInf(b, 0) || math.IsInf(c, 0) {
			return uint64(math.Float32bits(float32(math.FMA(a, b, c))))
		}
		// A binary32 product is exact in binary64 (at most 48 significant
		// bits, and its exponent fits). TwoSum recovers the rounding error
		// of adding c. Jam an inexact even binary64 result to an odd one in
		// the exact sum's direction before rounding to binary32. This avoids
		// a double-rounding error when the binary64 sum is a binary32 midpoint.
		product := a * b
		sum := product + c
		carried := sum - product
		residual := (product - (sum - carried)) + (c - carried)
		if residual != 0 && math.Float64bits(sum)&1 == 0 {
			direction := math.Inf(1)
			if residual < 0 {
				direction = math.Inf(-1)
			}
			sum = math.Nextafter(sum, direction)
		}
		return uint64(math.Float32bits(float32(sum)))
	}
	return math.Float64bits(math.FMA(math.Float64frombits(x), math.Float64frombits(y), math.Float64frombits(addend)))
}

func mixVerify(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ x>>31
}

func verifySymbol(name string, seed uint64) uint64 {
	hash := uint64(14695981039346656037)
	for i := 0; i < len(name); i++ {
		hash = (hash ^ uint64(name[i])) * 1099511628211
	}
	return mixVerify(hash ^ seed)
}

func verifyLabelInput(reg int, slot uint64, seed uint64) uint64 {
	if reg >= 0 {
		return mixVerify(seed ^ 0x96a96f19f0814a91 ^ uint64(reg))
	}
	return mixVerify(seed ^ slot)
}

type verifyMemory struct {
	nativeOrder ir.ByteOrder
	seed        uint64
	written     map[uint64]byte
	accesses    []verifyAccess
}

type verifyAccess struct {
	address uint64
	write   bool
}

func (m *verifyMemory) load8(address uint64) byte {
	m.accesses = append(m.accesses, verifyAccess{address: address})
	if value, ok := m.written[address]; ok {
		return value
	}
	return byte(mixVerify(address ^ m.seed))
}

func (m *verifyMemory) load64(address uint64, order ir.ByteOrder) uint64 {
	var value uint64
	for i := uint64(0); i < 8; i++ {
		shift := i * 8
		if order.Resolve(m.nativeOrder) == ir.ByteOrderBigEndian {
			shift = (7 - i) * 8
		}
		value |= uint64(m.load8(address+i)) << shift
	}
	return value
}

func (m *verifyMemory) store8(address uint64, value byte) {
	m.accesses = append(m.accesses, verifyAccess{address: address, write: true})
	m.written[address] = value
}

func (m *verifyMemory) store64(address, value uint64, order ir.ByteOrder) {
	for i := uint64(0); i < 8; i++ {
		shift := i * 8
		if order.Resolve(m.nativeOrder) == ir.ByteOrderBigEndian {
			shift = (7 - i) * 8
		}
		m.store8(address+i, byte(value>>shift))
	}
}

func sameVerifyMemory(left, right verifyMemory) bool {
	if len(left.written) != len(right.written) || len(left.accesses) != len(right.accesses) {
		return false
	}
	for i, access := range left.accesses {
		if right.accesses[i] != access {
			return false
		}
	}
	for address, value := range left.written {
		other, ok := right.written[address]
		if !ok || other != value {
			return false
		}
	}
	return true
}

func verifyIntegerMask(bits uint8) uint64 {
	if bits == 64 {
		return ^uint64(0)
	}
	return uint64(1)<<bits - 1
}

func verifyShiftBy(operation ir.Opcode, width uint8, value, count uint64) uint64 {
	if operation == ir.OpRotateLeft || operation == ir.OpRotateRight {
		count %= uint64(width)
	} else if count >= uint64(width) {
		if operation == ir.OpShiftRightArithmetic {
			return verifyShift(operation, width, value, width-1)
		}
		return 0
	}
	return verifyShift(operation, width, value, uint8(count))
}

func verifyMulHigh(width uint8, a, b uint64) uint64 {
	mask := verifyIntegerMask(width)
	a, b = a&mask, b&mask
	if width == 64 {
		high, _ := bits.Mul64(a, b)
		return high
	}
	return (a * b) >> width & mask
}

func verifyWordCarry(op ir.Opcode, width uint8, a, b, c uint64) uint64 {
	mask := verifyIntegerMask(width)
	a, b = a&mask, b&mask
	switch op {
	case ir.OpCarryAdd:
		if width == 64 {
			_, carry := bits.Add64(a, b, 0)
			return carry
		}
		return (a + b) >> width
	case ir.OpBorrowSub:
		if a < b {
			return 1
		}
		return 0
	case ir.OpAddWithCarry:
		return (a + b + (c & 1)) & mask
	case ir.OpSubWithBorrow:
		return (a - b - (c & 1)) & mask
	default:
		panic("invalid word carry operation")
	}
}

func verifyCountZeros(op ir.Opcode, width uint8, x uint64) uint64 {
	x &= verifyIntegerMask(width)
	if op == ir.OpLeadingZeros {
		return uint64(bits.LeadingZeros64(x) - (64 - int(width)))
	}
	n := bits.TrailingZeros64(x)
	if n > int(width) {
		n = int(width)
	}
	return uint64(n)
}
