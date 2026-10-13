package compile

import (
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

// errLoweringMismatch means instruction selection changed IR semantics.
var errLoweringMismatch = errors.New("nyanjit: lowered instructions do not match the builder")

// verifySelectedProgram interprets both sides of instruction selection with
// deterministic scalar inputs and a byte-addressed native-memory model. It is
// a compile-time guard against incorrect rewrites. Fragments with calls or
// syscalls are skipped because their external effects are not modeled here.
func verifySelectedProgram(source []ir.Instruction, selected machine.SelectedProgram, result, maxArgs int, registerBits uint8, nativeOrder ir.ByteOrder) error {
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
	fusedAdds := make(map[int]machine.SelectedInstruction)
	fusedProducts := make(map[int]bool)
	for _, ins := range selected.Instructions {
		if ins.Op == machine.SelectedFloatFMA && ins.Dst >= 0 && ins.Dst < len(source) && source[ins.Dst].Op == ir.OpFProductError {
			fusedProducts[ins.Dst] = true
		}
		if ins.Op == machine.SelectedFloatFMA && ins.Dst >= 0 && ins.Dst < len(source) && source[ins.Dst].Op == ir.OpFAdd {
			fusedAdds[ins.Dst] = ins
		}
	}
	inputs := make([]uint64, maxArgs)
	originalMemory := verifyMemory{written: make(map[uint64]byte), nativeOrder: nativeOrder}
	selectedMemory := verifyMemory{written: make(map[uint64]byte), nativeOrder: nativeOrder}
	for sample := uint64(1); sample <= 5; sample++ {
		for i := range inputs {
			inputs[i] = mixVerify(sample*0x9e3779b97f4a7c15 + uint64(i+1))
		}
		initial := sample * 0x517cc1b727220a95
		clear(originalMemory.written)
		clear(selectedMemory.written)
		originalMemory.accesses = originalMemory.accesses[:0]
		selectedMemory.accesses = selectedMemory.accesses[:0]
		originalMemory.seed, selectedMemory.seed = initial, initial
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
				// Narrow values, label parameters included, arrive
				// zero-extended, as every narrow register value is.
				original[id] = verifyLabelInput(ins.EntryReg, ins.Imm, initial) & verifyIntegerMask(selectedInstructionBits(ins))
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
			case ir.OpMulHighSigned:
				original[id] = verifyMulHighSigned(selectedInstructionBits(ins), original[ins.A], original[ins.B])
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
			case ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned:
				original[id] = verifyDivide(ins.Op, ir.DivisionSemantics(ins.Imm), selectedInstructionBits(ins), original[ins.A], original[ins.B])
			case ir.OpFFromSigned, ir.OpFFromUnsigned:
				original[id] = verifyIntToFloat(ins.Op, selectedInstructionBits(source[ins.A]), ins.Bits, original[ins.A])
			case ir.OpFCompare:
				original[id] = verifyFloatCompare(ir.FloatCondition(ins.Imm), ins.Bits, original[ins.A], original[ins.B])
			case ir.OpFToSigned, ir.OpFToUnsigned:
				original[id] = verifyFloatToInt(ins.Op, ir.ConversionSemantics(ins.Imm), selectedInstructionBits(source[ins.A]), ins.Bits, original[ins.A])
			case ir.OpCastUnsigned:
				original[id] = original[ins.A] & verifyIntegerMask(selectedInstructionBits(ins))
			case ir.OpSignExtend:
				width := uint8(ins.Imm)
				low := original[ins.A] & verifyIntegerMask(width)
				original[id] = uint64(int64(low<<(64-width))>>(64-width)) & verifyIntegerMask(selectedInstructionBits(ins))
			case ir.OpAddScaled:
				original[id] = (original[ins.A] + original[ins.B]*ins.Imm) & verifyIntegerMask(selectedInstructionBits(ins))
			case ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy:
				operation, _ := op.ShiftByOperation(ins.Op)
				original[id] = verifyShiftBy(operation, selectedInstructionBits(ins), original[ins.A], original[ins.B])
			case ir.OpShiftRight, ir.OpShiftLeft, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight:
				original[id] = verifyShift(ins.Op, selectedInstructionBits(ins), original[ins.A], uint8(ins.Imm))
			case ir.OpSelect:
				if original[ins.A] != 0 {
					original[id] = original[ins.B]
				} else {
					original[id] = original[ins.C]
				}
			case ir.OpLoad16:
				original[id] = originalMemory.loadN(original[ins.A], 16, ins.ByteOrder)
			case ir.OpLoad32:
				original[id] = originalMemory.loadN(original[ins.A], 32, ins.ByteOrder)
			case ir.OpLoad8:
				original[id] = uint64(originalMemory.load8(original[ins.A]))
			case ir.OpLoad64:
				original[id] = originalMemory.load64(original[ins.A], ins.ByteOrder)
			case ir.OpStore16:
				originalMemory.storeN(original[ins.A], original[ins.B], 16, ins.ByteOrder)
			case ir.OpStore32:
				originalMemory.storeN(original[ins.A], original[ins.B], 32, ins.ByteOrder)
			case ir.OpStore8:
				originalMemory.store8(original[ins.A], byte(original[ins.B]))
			case ir.OpStore64:
				originalMemory.store64(original[ins.A], original[ins.B], ins.ByteOrder)
			default:
				return backend.ErrInvalidFunction
			}
		}
		lowered := make([]uint64, len(selected.Values))
		for id, value := range selected.Values {
			if value.Param >= 0 {
				lowered[id] = inputs[value.Param]
			} else if value.EntrySlot >= 0 {
				lowered[id] = verifyLabelInput(-1, uint64(value.EntrySlot), initial) & verifyValueMask(value)
			} else if value.EntryReg >= 0 {
				lowered[id] = verifyLabelInput(value.EntryReg, 0, initial) & verifyValueMask(value)
			}
		}
		for _, ins := range selected.Instructions {
			switch ins.Op {
			case machine.SelectedConst:
				lowered[ins.Dst] = ins.Imm
			case machine.SelectedSymbol:
				lowered[ins.Dst] = verifySymbol(ins.Symbol, initial)
			case machine.SelectedMove:
				lowered[ins.Dst] = lowered[ins.A]
			case machine.SelectedBinary:
				width := ins.Bits
				if width == 0 {
					width = uint8(selected.Values[ins.Dst].Words) * registerBits
				}
				lowered[ins.Dst] = verifyBinary(ins.Arithmetic, lowered[ins.A], lowered[ins.B]) & verifyIntegerMask(width)
			case machine.SelectedFloatBinary:
				lowered[ins.Dst] = verifyFloat(ins.Arithmetic, ins.Bits, lowered[ins.A], lowered[ins.B])
			case machine.SelectedFloatFMA:
				lowered[ins.Dst] = verifyFMA(ins.Bits, lowered[ins.A], lowered[ins.B], lowered[ins.C])
			case machine.SelectedAndNot:
				lowered[ins.Dst] = (lowered[ins.A] &^ lowered[ins.B]) & verifyIntegerMask(ins.Bits)
			case machine.SelectedCountZeros:
				lowered[ins.Dst] = verifyCountZeros(ins.Arithmetic, ins.Bits, lowered[ins.A])
			case machine.SelectedFloatUnary:
				lowered[ins.Dst] = verifyFloatUnary(ins.Arithmetic, ins.Bits, lowered[ins.A])
			case machine.SelectedDivide:
				lowered[ins.Dst] = verifyDivide(ins.Arithmetic, ir.DivisionSemantics(ins.Imm), ins.Bits, lowered[ins.A], lowered[ins.B])
			case machine.SelectedIntToFloat:
				lowered[ins.Dst] = verifyIntToFloat(ins.Arithmetic, uint8(ins.Imm), ins.Bits, lowered[ins.A])
			case machine.SelectedFloatCompare:
				lowered[ins.Dst] = verifyFloatCompare(ir.FloatCondition(ins.Imm), ins.Bits, lowered[ins.A], lowered[ins.B])
			case machine.SelectedFloatToInt:
				lowered[ins.Dst] = verifyFloatToInt(ins.Arithmetic, ir.ConversionSemantics(ins.Imm), uint8(ins.Disp), ins.Bits, lowered[ins.A])
			case machine.SelectedBinaryImmediate:
				lowered[ins.Dst] = verifyBinary(ins.Arithmetic, lowered[ins.A], ins.Imm)
				if ins.Bits == registerBits || ins.ZeroExtended {
					lowered[ins.Dst] &= verifyIntegerMask(ins.Bits)
				}
			case machine.SelectedDec:
				lowered[ins.Dst] = (lowered[ins.A] - 1) & verifyIntegerMask(registerBits)
			case machine.SelectedInc:
				lowered[ins.Dst] = (lowered[ins.A] + 1) & verifyIntegerMask(registerBits)
			case machine.SelectedNarrow:
				lowered[ins.Dst] = lowered[ins.A] & verifyIntegerMask(uint8(ins.Imm))
			case machine.SelectedSignExtend:
				width := uint8(ins.Imm)
				low := lowered[ins.A] & verifyIntegerMask(width)
				lowered[ins.Dst] = uint64(int64(low<<(64-width)) >> (64 - width))
			case machine.SelectedCarryBit:
				op := ir.OpCarryAdd
				if ins.Arithmetic == ir.OpSub {
					op = ir.OpBorrowSub
				}
				lowered[ins.Dst] = verifyWordCarry(op, ins.Bits, lowered[ins.A], lowered[ins.B], 0)
			case machine.SelectedWordCarry:
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
			case machine.SelectedMulHigh:
				lowered[ins.Dst] = verifyMulHigh(ins.Bits, lowered[ins.A], lowered[ins.B])
			case machine.SelectedMulHighSigned:
				lowered[ins.Dst] = verifyMulHighSigned(ins.Bits, lowered[ins.A], lowered[ins.B])
			case machine.SelectedShiftBy:
				lowered[ins.Dst] = verifyShiftBy(ins.Arithmetic, ins.Bits, lowered[ins.A], lowered[ins.B])
			case machine.SelectedShift:
				lowered[ins.Dst] = verifyShift(ins.Arithmetic, ins.Bits, lowered[ins.A], uint8(ins.Imm))
			case machine.SelectedScaled:
				lowered[ins.Dst] = (lowered[ins.A] + lowered[ins.B]*ins.Imm + ins.Disp) & verifyIntegerMask(uint8(selected.Values[ins.Dst].Words)*registerBits)
			case machine.SelectedMulAdd:
				lowered[ins.Dst] = lowered[ins.A] + lowered[ins.B]*lowered[ins.C]
			case machine.SelectedSelect:
				if lowered[ins.A] != 0 {
					lowered[ins.Dst] = lowered[ins.B]
				} else {
					lowered[ins.Dst] = lowered[ins.C]
				}
			case machine.SelectedLoad16:
				lowered[ins.Dst] = selectedMemory.loadN(lowered[ins.A], 16, ins.ByteOrder)
			case machine.SelectedLoad32:
				lowered[ins.Dst] = selectedMemory.loadN(lowered[ins.A], 32, ins.ByteOrder)
			case machine.SelectedLoad8:
				lowered[ins.Dst] = uint64(selectedMemory.load8(lowered[ins.A]))
			case machine.SelectedLoad64:
				lowered[ins.Dst] = selectedMemory.load64(lowered[ins.A], ins.ByteOrder)
			case machine.SelectedLoadAddress:
				lowered[ins.Dst] = selectedMemory.load64(lowered[ins.A]+lowered[ins.B]*ins.Imm+ins.Disp, ins.ByteOrder)
			case machine.SelectedLoadOffset:
				lowered[ins.Dst] = selectedMemory.load64(lowered[ins.A]+ins.Disp, ins.ByteOrder)
			case machine.SelectedStore16:
				selectedMemory.storeN(lowered[ins.A], lowered[ins.B], 16, ins.ByteOrder)
			case machine.SelectedStore32:
				selectedMemory.storeN(lowered[ins.A], lowered[ins.B], 32, ins.ByteOrder)
			case machine.SelectedStore8:
				selectedMemory.store8(lowered[ins.A], byte(lowered[ins.B]))
			case machine.SelectedStore64:
				selectedMemory.store64(lowered[ins.A], lowered[ins.B], ins.ByteOrder)
			case machine.SelectedStoreAddress:
				selectedMemory.store64(lowered[ins.A]+lowered[ins.B]*ins.Imm+ins.Disp, lowered[ins.C], ins.ByteOrder)
			case machine.SelectedStoreOffset:
				selectedMemory.store64(lowered[ins.A]+ins.Disp, lowered[ins.B], ins.ByteOrder)
			default:
				return backend.ErrInvalidFunction
			}
		}
		if original[result] != lowered[selected.Result] {
			return fmt.Errorf("%w: sample %d result %d: IR %#x, selected %#x", errLoweringMismatch, sample, result, original[result], lowered[selected.Result])
		}
		if !sameVerifyMemory(originalMemory, selectedMemory) {
			return fmt.Errorf("%w: sample %d memory effects differ", errLoweringMismatch, sample)
		}
		for _, id := range selected.Exports {
			if original[id] != lowered[id] {
				return fmt.Errorf("%w: sample %d export %d: IR %#x, selected %#x", errLoweringMismatch, sample, id, original[id], lowered[id])
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

// verifyDivide divides width-bit a by b as operation does with semantics s.
// An unchecked division the IR leaves undefined gives 0 on both sides.
func verifyDivide(operation ir.Opcode, s ir.DivisionSemantics, width uint8, a, b uint64) uint64 {
	mask := verifyIntegerMask(width)
	a, b = a&mask, b&mask
	rem := operation == ir.OpRem || operation == ir.OpRemSigned
	if b == 0 {
		switch {
		case s == ir.DivisionUnchecked:
			return 0
		case rem:
			return a
		case s == ir.DivisionAllOnes:
			return mask
		}
		return 0
	}
	switch operation {
	case ir.OpDiv:
		return a / b
	case ir.OpRem:
		return a % b
	}
	shift := 64 - width
	sa, sb := int64(a<<shift)>>shift, int64(b<<shift)>>shift
	if sb == -1 && sa == -1<<(width-1) {
		if rem || s == ir.DivisionUnchecked {
			return 0
		}
		return a
	}
	if rem {
		return uint64(sa%sb) & mask
	}
	return uint64(sa/sb) & mask
}

// verifyIntToFloat converts the in-bit integer a to a binary32 or binary64
// (out) bit pattern.
func verifyIntToFloat(operation ir.Opcode, in, out uint8, a uint64) uint64 {
	a &= verifyIntegerMask(in)
	var f float64
	var f32 float32
	if operation == ir.OpFFromSigned {
		v := int64(a<<(64-in)) >> (64 - in)
		f, f32 = float64(v), float32(v)
	} else {
		f, f32 = float64(a), float32(a)
	}
	if out == 32 {
		return uint64(math.Float32bits(f32))
	}
	return math.Float64bits(f)
}

// verifyFloatToInt truncates the in-bit float a toward zero to an out-bit
// integer with semantics s. A native conversion the IR leaves undefined
// gives 0 on both sides.
func verifyFloatToInt(operation ir.Opcode, s ir.ConversionSemantics, in, out uint8, a uint64) uint64 {
	f := math.Float64frombits(a)
	if in == 32 {
		f = float64(math.Float32frombits(uint32(a)))
	}
	mask := verifyIntegerMask(out)
	if math.IsNaN(f) {
		return 0
	}
	t := math.Trunc(f)
	saturate := s == ir.ConversionSaturate
	if operation == ir.OpFToSigned {
		switch bound := math.Ldexp(1, int(out)-1); {
		case t < -bound && saturate:
			return uint64(1) << (out - 1)
		case t >= bound && saturate:
			return mask >> 1
		case t < -bound || t >= bound:
			return 0
		}
		return uint64(int64(t)) & mask
	}
	switch bound := math.Ldexp(1, int(out)); {
	case t < 0 && saturate:
		return 0
	case t >= bound && saturate:
		return mask
	case t < 0 || t >= bound:
		return 0
	}
	return uint64(t) & mask
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

// verifyValueMask is the zero-extended image of a selected value's logical
// width.
func verifyValueMask(value machine.SelectedValue) uint64 {
	if value.Bits == 0 || value.Bits >= 64 {
		return math.MaxUint64
	}
	return verifyIntegerMask(value.Bits)
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

func verifyMulHighSigned(width uint8, a, b uint64) uint64 {
	mask := verifyIntegerMask(width)
	a, b = a&mask, b&mask
	if width == 64 {
		high, _ := bits.Mul64(a, b)
		return high - a&-(b>>63) - b&-(a>>63)
	}
	shift := 64 - width
	x, y := int64(a<<shift)>>shift, int64(b<<shift)>>shift
	return uint64(x*y>>width) & mask
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

func (m *verifyMemory) loadN(a uint64, bits uint8, order ir.ByteOrder) uint64 {
	var v uint64
	for n := uint8(0); n < bits/8; n++ {
		shift := 8 * n
		if order.Resolve(m.nativeOrder) == ir.ByteOrderBigEndian {
			shift = bits - 8 - shift
		}
		v |= uint64(m.load8(a+uint64(n))) << shift
	}
	return v
}
func (m *verifyMemory) storeN(a, v uint64, bits uint8, order ir.ByteOrder) {
	for n := uint8(0); n < bits/8; n++ {
		shift := 8 * n
		if order.Resolve(m.nativeOrder) == ir.ByteOrderBigEndian {
			shift = bits - 8 - shift
		}
		m.store8(a+uint64(n), byte(v>>shift))
	}
}

// verifyFloatCompare evaluates condition c for the w-bit floats a and b.
// Widening binary32 to binary64 is exact, so it preserves every relation.
func verifyFloatCompare(c ir.FloatCondition, w uint8, a, b uint64) uint64 {
	p, q := math.Float64frombits(a), math.Float64frombits(b)
	if w == 32 {
		p, q = float64(math.Float32frombits(uint32(a))), float64(math.Float32frombits(uint32(b)))
	}
	var r bool
	switch c &^ 1 {
	case ir.FloatEqual:
		r = p == q
	case ir.FloatLess:
		r = p < q
	case ir.FloatLessEqual:
		r = p <= q
	case ir.FloatGreater:
		r = p > q
	case ir.FloatGreaterEqual:
		r = p >= q
	case ir.FloatOrdered:
		r = p == p && q == q
	case ir.FloatOrderedNotEqual:
		r = p < q || p > q
	}
	if r != (c&1 != 0) {
		return 1
	}
	return 0
}
