package op

import "strconv"

// Opcode names a target-independent operation.
type Opcode uint8

const (
	// OpParam is the next entry parameter, in instruction order. In a
	// control program it must be in the entry segment.
	OpParam Opcode = iota
	// OpConst is the constant Imm, which must fit in Bits. With Symbol set,
	// the value is bound at compile or link time.
	OpConst
	// OpAdd, OpSub and OpMul wrap modulo 2^Bits; OpMul keeps the low half.
	// A and B have the instruction's width (lane-wise for vectors).
	OpAdd
	OpSub
	OpMul
	// OpAnd, OpOr and OpXor are bitwise operations on same-width A and B.
	OpAnd
	OpOr
	OpXor
	// OpSelect returns B when scalar A is nonzero and C otherwise.
	OpSelect
	// OpAddScaled returns A + B*Imm modulo 2^Bits.
	OpAddScaled
	// OpLoad64 loads 64 bits from address A (Native or LittleEndian order).
	OpLoad64
	// OpStore64 stores 64-bit B at address A. It defines no value.
	OpStore64
	// OpLoad8 loads a byte from address A, zero-extended to Bits (8 or 64).
	OpLoad8
	// OpStore8 stores the low byte of B at address A. It defines no value.
	OpStore8
	// OpSyscall makes a raw Linux system call: Args[0] is the number and up
	// to six more Args are its arguments. It returns the raw result.
	OpSyscall
	// OpCallC calls the native address Args[0] with the C ABI, passing
	// Args[1:], and returns the integer result register.
	OpCallC
	// OpCallGo calls the Go function code pointer Args[0] with scalar
	// arguments Args[1:] and returns the integer result register.
	OpCallGo
	// OpLabelParam reads parameter Imm of the label beginning its block.
	OpLabelParam
	// OpShiftRight shifts A right logically by Imm, which is below the
	// scalar width. Vector counts at least the lane width give zero.
	OpShiftRight
	// OpCastUnsigned truncates or zero-extends A to Bits.
	OpCastUnsigned
	// OpSignExtend sign-extends the low Imm (8, 16 or 32) bits of A to Bits.
	OpSignExtend
	// OpShiftLeft shifts A left by Imm, which is below the scalar width.
	// Vector counts at least the lane width give zero.
	OpShiftLeft
	// OpShiftRightArithmetic shifts A right by Imm, filling with its sign bit.
	OpShiftRightArithmetic
	// OpRotateLeft and OpRotateRight rotate A by Imm (vectors: modulo the
	// lane width).
	OpRotateLeft
	OpRotateRight
	// Floating point arithmetic consumes and produces IEEE 754 bit patterns.
	// Bits must be 32 or 64. Register-bank assignment is deferred to lowering.
	// Results are rounded in the target's current floating point environment,
	// and NaN payloads are target-dependent.
	//
	// OpFAdd returns A+B. Contract permits fusing a single-use OpFMul operand.
	OpFAdd
	// OpFSub returns A-B.
	OpFSub
	// OpFMul returns A*B.
	OpFMul
	// OpFDiv returns A/B.
	OpFDiv
	// OpFMA returns A*B+C with one rounding. It requires hardware FMA.
	OpFMA
	// OpFSqrt returns the square root of A; -0 gives -0, and a negative
	// nonzero operand gives NaN.
	OpFSqrt
	// OpFConvert converts binary64 A to binary32 (Bits 32) or binary32 A to
	// binary64 (Bits 64).
	OpFConvert
	// Value-count shifts saturate at the value width; rotations reduce the
	// unsigned count modulo that width. A is the value and B is the count.
	//
	// OpShiftLeftBy shifts A left by B; counts at least the width give zero.
	OpShiftLeftBy
	// OpShiftRightBy shifts A right logically by B; counts at least the
	// width give zero.
	OpShiftRightBy
	// OpShiftRightArithmeticBy shifts A right by B with sign fill; counts at
	// least the width give all sign bits.
	OpShiftRightArithmeticBy
	// OpRotateLeftBy and OpRotateRightBy rotate A by B modulo the width.
	OpRotateLeftBy
	OpRotateRightBy
	// OpMulHigh returns the upper Bits of the full unsigned product A*B.
	OpMulHigh
	// Carry/borrow extraction returns zero or one at the operand width.
	//
	// OpCarryAdd returns the carry out of A+B.
	OpCarryAdd
	// OpBorrowSub returns 1 when A < B unsigned.
	OpBorrowSub
	// These operations add/subtract the low bit of C with A and B.
	//
	// OpAddWithCarry returns A+B+(C&1) modulo 2^Bits.
	OpAddWithCarry
	// OpSubWithBorrow returns A-B-(C&1) modulo 2^Bits.
	OpSubWithBorrow
	// OpVLoad loads a vector of Type from address A at any alignment, lane
	// zero at the lowest address.
	OpVLoad
	// OpVStore stores vector B at address A at any alignment. It defines no
	// value.
	OpVStore
	// OpVSplat repeats scalar A in every lane.
	OpVSplat
	// OpVConst is the vector in Data: lane zero first, each lane little endian.
	OpVConst
	// OpVExtract returns lane Imm of vector A.
	OpVExtract
	// OpVInsert returns A with lane Imm replaced by scalar B.
	OpVInsert
	// OpVCompare compares vectors A and B lane by lane with condition Imm
	// and returns a mask. Bits is the input lane width.
	OpVCompare
	// OpVMaskBits returns a scalar whose bit i is mask A's lane i.
	OpVMaskBits
	// OpVZero is the all-zero vector of Type.
	OpVZero
	// OpVInsertZero returns A with lane Imm replaced by scalar B, as
	// OpVInsert does. The scalarizer uses it to build vectors from OpVZero.
	OpVInsertZero
	// Bit counts return Bits for a zero operand. A is the same-width scalar input.
	//
	// OpLeadingZeros counts the zero bits above A's highest set bit.
	OpLeadingZeros
	// OpTrailingZeros counts the zero bits below A's lowest set bit.
	OpTrailingZeros
	// OpFProductError returns the binary64 residual A*B-C of an exact product.
	// C must be A*B rounded to binary64, and A/B must be finite and well scaled:
	// multiplication by 2^27+1 and all Dekker intermediate products/subtractions
	// must avoid overflow and underflow. Exact zero intermediates are allowed.
	// Builder's inline math arranges this contract. Validate checks references
	// and widths, not these runtime numeric preconditions. Results outside the
	// contract are unspecified and may differ by CPU profile (including NaN on
	// Dekker targets versus zero on FMA targets for large split-overflow inputs).
	// Selection uses FMA when available, otherwise separately rounded Dekker steps.
	OpFProductError
	// Scoped storage and straight-line scope boundaries.
	//
	// OpGet reads a snapshot of part Imm of Variable.
	OpGet
	// OpSet stores A into part Imm of Variable. It defines no value.
	OpSet
	// OpScopeEnter enters scope Imm; Args are the scope's captures.
	OpScopeEnter
	// OpScopeLeave leaves the innermost scope, Imm.
	OpScopeLeave
	// OpVCarrylessMul multiplies one selected 64-bit polynomial from each
	// 128-bit chunk of A and B, without carry or polynomial reduction. Type
	// has an even number of 64-bit lanes. Each output chunk holds the low
	// and high words of the 128-bit product. Imm bit 0 selects A's lane and
	// bit 1 selects B's lane within each input chunk; no other bits are valid.
	OpVCarrylessMul
	// Narrow memory ops require Bits equal to their access width. Loads return
	// a zero-extended scalar of that width; stores truncate B and define no value.
	//
	// OpLoad16 loads 16 bits from address A.
	OpLoad16
	// OpStore16 stores the low 16 bits of B at address A.
	OpStore16
	// OpLoad32 loads 32 bits from address A.
	OpLoad32
	// OpStore32 stores the low 32 bits of B at address A.
	OpStore32
	// Integer division divides same-width scalars A by B (8, 16, 32 or 64
	// bits) toward zero; Imm is the DivisionSemantics for a zero divisor and
	// for signed MIN / -1. A remainder has the dividend's sign.
	//
	// OpDiv returns A/B unsigned.
	OpDiv
	// OpDivSigned returns A/B signed.
	OpDivSigned
	// OpRem returns A%B unsigned.
	OpRem
	// OpRemSigned returns A%B signed.
	OpRemSigned
	// Integer to floating point conversions convert integer A (32 or 64
	// bits) to binary32 or binary64 (Bits), rounding in the current mode.
	//
	// OpFFromSigned converts signed A.
	OpFFromSigned
	// OpFFromUnsigned converts unsigned A.
	OpFFromUnsigned
	// Floating point to integer conversions truncate binary32 or binary64 A
	// toward zero to an integer of Bits (32 or 64); Imm is the
	// ConversionSemantics for NaN and out-of-range values.
	//
	// OpFToSigned converts to a signed integer.
	OpFToSigned
	// OpFToUnsigned converts to an unsigned integer.
	OpFToUnsigned
	// OpMulHighSigned returns the upper Bits of the full signed product A*B.
	OpMulHighSigned
	// OpFCompare compares binary32 or binary64 A with B, same width, by the
	// FloatCondition in Imm. It returns 1 or 0 at width Bits.
	OpFCompare

	opcodeEnd // Keep last: the exclusive upper bound of valid opcodes.
)

// ProducesValue reports whether an instruction with op defines an SSA value.
// Unknown opcodes do not produce values.
func (op Opcode) ProducesValue() bool {
	switch op {
	case OpSet, OpScopeEnter, OpScopeLeave, OpStore64, OpStore8, OpStore16, OpStore32, OpVStore:
		return false
	}
	return op < opcodeEnd
}

// String returns the operation name, or a numeric name for an unknown opcode.
func (op Opcode) String() string {
	names := [...]string{
		OpParam:                  "OpParam",
		OpConst:                  "OpConst",
		OpAdd:                    "OpAdd",
		OpSub:                    "OpSub",
		OpMul:                    "OpMul",
		OpAnd:                    "OpAnd",
		OpOr:                     "OpOr",
		OpXor:                    "OpXor",
		OpSelect:                 "OpSelect",
		OpAddScaled:              "OpAddScaled",
		OpLoad64:                 "OpLoad64",
		OpStore64:                "OpStore64",
		OpLoad8:                  "OpLoad8",
		OpLoad16:                 "OpLoad16",
		OpStore16:                "OpStore16",
		OpLoad32:                 "OpLoad32",
		OpStore32:                "OpStore32",
		OpStore8:                 "OpStore8",
		OpSyscall:                "OpSyscall",
		OpCallC:                  "OpCallC",
		OpCallGo:                 "OpCallGo",
		OpLabelParam:             "OpLabelParam",
		OpShiftRight:             "OpShiftRight",
		OpCastUnsigned:           "OpCastUnsigned",
		OpSignExtend:             "OpSignExtend",
		OpShiftLeft:              "OpShiftLeft",
		OpShiftRightArithmetic:   "OpShiftRightArithmetic",
		OpRotateLeft:             "OpRotateLeft",
		OpRotateRight:            "OpRotateRight",
		OpFAdd:                   "OpFAdd",
		OpFSub:                   "OpFSub",
		OpFMul:                   "OpFMul",
		OpFDiv:                   "OpFDiv",
		OpFMA:                    "OpFMA",
		OpFSqrt:                  "OpFSqrt",
		OpFConvert:               "OpFConvert",
		OpShiftLeftBy:            "OpShiftLeftBy",
		OpShiftRightBy:           "OpShiftRightBy",
		OpShiftRightArithmeticBy: "OpShiftRightArithmeticBy",
		OpRotateLeftBy:           "OpRotateLeftBy",
		OpRotateRightBy:          "OpRotateRightBy",
		OpMulHigh:                "OpMulHigh",
		OpCarryAdd:               "OpCarryAdd",
		OpBorrowSub:              "OpBorrowSub",
		OpAddWithCarry:           "OpAddWithCarry",
		OpSubWithBorrow:          "OpSubWithBorrow",
		OpVLoad:                  "OpVLoad",
		OpVStore:                 "OpVStore",
		OpVSplat:                 "OpVSplat",
		OpVConst:                 "OpVConst",
		OpVExtract:               "OpVExtract",
		OpVInsert:                "OpVInsert",
		OpVCompare:               "OpVCompare",
		OpVMaskBits:              "OpVMaskBits",
		OpVZero:                  "OpVZero",
		OpVInsertZero:            "OpVInsertZero",
		OpLeadingZeros:           "OpLeadingZeros",
		OpTrailingZeros:          "OpTrailingZeros",
		OpFProductError:          "OpFProductError",
		OpGet:                    "OpGet",
		OpSet:                    "OpSet",
		OpScopeEnter:             "OpScopeEnter",
		OpScopeLeave:             "OpScopeLeave",
		OpVCarrylessMul:          "OpVCarrylessMul",
		OpDiv:                    "OpDiv",
		OpDivSigned:              "OpDivSigned",
		OpRem:                    "OpRem",
		OpRemSigned:              "OpRemSigned",
		OpFFromSigned:            "OpFFromSigned",
		OpFFromUnsigned:          "OpFFromUnsigned",
		OpFToSigned:              "OpFToSigned",
		OpFToUnsigned:            "OpFToUnsigned",
		OpMulHighSigned:          "OpMulHighSigned",
		OpFCompare:               "OpFCompare",
	}
	if int(op) < len(names) && names[op] != "" {
		return names[op]
	}
	return "Opcode(" + strconv.Itoa(int(op)) + ")"
}

// ShiftByOperation identifies a value-count shift and its immediate form.
func ShiftByOperation(operation Opcode) (Opcode, bool) {
	switch operation {
	case OpShiftLeftBy:
		return OpShiftLeft, true
	case OpShiftRightBy:
		return OpShiftRight, true
	case OpShiftRightArithmeticBy:
		return OpShiftRightArithmetic, true
	case OpRotateLeftBy:
		return OpRotateLeft, true
	case OpRotateRightBy:
		return OpRotateRight, true
	default:
		return 0, false
	}
}
