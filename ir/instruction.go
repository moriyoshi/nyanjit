// Package ir contains the target-independent instructions produced by a Builder.
package ir

import "github.com/moriyoshi/nyanjit/abi"

// Opcode names a target-independent operation.
type Opcode uint8

const (
	OpParam Opcode = iota
	OpConst
	OpAdd
	OpSub
	OpMul
	OpAnd
	OpOr
	OpXor
	OpSelect
	OpAddScaled
	OpLoad64
	OpStore64
	OpLoad8
	OpStore8
	OpSyscall
	OpCallC
	OpCallGo
	OpLabelParam
	OpShiftRight
	OpCastUnsigned
	OpSignExtend
	OpShiftLeft
	OpShiftRightArithmetic
	OpRotateLeft
	OpRotateRight
	// Floating point arithmetic consumes and produces IEEE 754 bit patterns.
	// Bits must be 32 or 64. Register-bank assignment is deferred to lowering.
	OpFAdd
	OpFSub
	OpFMul
	OpFDiv
	OpFMA
	OpFSqrt
	OpFConvert
	// Value-count shifts saturate at the value width; rotations reduce the
	// unsigned count modulo that width. A is the value and B is the count.
	OpShiftLeftBy
	OpShiftRightBy
	OpShiftRightArithmeticBy
	OpRotateLeftBy
	OpRotateRightBy
	// OpMulHigh returns the upper Bits of the full unsigned product A*B.
	OpMulHigh
	// Carry/borrow extraction returns zero or one at the operand width.
	OpCarryAdd
	OpBorrowSub
	// These operations add/subtract the low bit of C with A and B.
	OpAddWithCarry
	OpSubWithBorrow
	OpVLoad
	OpVStore
	OpVSplat
	OpVConst
	OpVExtract
	OpVInsert
	OpVCompare
	OpVMaskBits
	OpVZero
	OpVInsertZero
	// Bit counts return Bits for a zero operand. A is the same-width scalar input.
	OpLeadingZeros
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
	OpGet
	OpSet
	OpScopeEnter
	OpScopeLeave
)

// Instruction is a semantic operation. A, B, C, and Args refer to preceding
// instruction indices in the same Program. Immediate values use their uint64
// bit pattern; Bits zero denotes the default 64-bit width.
//
// Programs have public mutable fields. Clone an Instruction when it must be
// independent of its Args and Effect data.
type Instruction struct {
	Variable int // OpGet/OpSet variable-table index.
	Type     Type
	Data     []byte
	Op       Opcode
	Bits     uint8
	Symbol   string
	EntryReg int
	A        int
	B        int
	C        int
	Imm      uint64
	// ByteOrder applies to loads and stores. Native is the default; explicit
	// orders survive addressing-mode selection and register allocation.
	ByteOrder ByteOrder
	// Contract permits a single-use FMul input of FAdd to be contracted to FMA.
	// It is set only for deferred expression trees, where reassociation is allowed.
	Contract bool
	Args     []int
	Effect   *abi.CallEffect
}

// Clone copies an instruction's variable-length data.
func (ins Instruction) Clone() Instruction {
	ins.Args = append([]int(nil), ins.Args...)
	ins.Data = append([]byte(nil), ins.Data...)
	if ins.Effect != nil {
		effect := ins.Effect.Clone()
		ins.Effect = &effect
	}
	return ins
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
