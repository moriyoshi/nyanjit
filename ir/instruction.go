package ir

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Opcode names a target-independent operation. It is op.Opcode; the
// operations are documented there.
type Opcode = op.Opcode

// The operations of op.Opcode, re-exported for IR code.
const (
	OpParam                  = op.OpParam
	OpConst                  = op.OpConst
	OpAdd                    = op.OpAdd
	OpSub                    = op.OpSub
	OpMul                    = op.OpMul
	OpAnd                    = op.OpAnd
	OpOr                     = op.OpOr
	OpXor                    = op.OpXor
	OpSelect                 = op.OpSelect
	OpAddScaled              = op.OpAddScaled
	OpLoad64                 = op.OpLoad64
	OpStore64                = op.OpStore64
	OpLoad8                  = op.OpLoad8
	OpStore8                 = op.OpStore8
	OpSyscall                = op.OpSyscall
	OpCallC                  = op.OpCallC
	OpCallGo                 = op.OpCallGo
	OpLabelParam             = op.OpLabelParam
	OpShiftRight             = op.OpShiftRight
	OpCastUnsigned           = op.OpCastUnsigned
	OpSignExtend             = op.OpSignExtend
	OpShiftLeft              = op.OpShiftLeft
	OpShiftRightArithmetic   = op.OpShiftRightArithmetic
	OpRotateLeft             = op.OpRotateLeft
	OpRotateRight            = op.OpRotateRight
	OpFAdd                   = op.OpFAdd
	OpFSub                   = op.OpFSub
	OpFMul                   = op.OpFMul
	OpFDiv                   = op.OpFDiv
	OpFMA                    = op.OpFMA
	OpFSqrt                  = op.OpFSqrt
	OpFConvert               = op.OpFConvert
	OpShiftLeftBy            = op.OpShiftLeftBy
	OpShiftRightBy           = op.OpShiftRightBy
	OpShiftRightArithmeticBy = op.OpShiftRightArithmeticBy
	OpRotateLeftBy           = op.OpRotateLeftBy
	OpRotateRightBy          = op.OpRotateRightBy
	OpMulHigh                = op.OpMulHigh
	OpCarryAdd               = op.OpCarryAdd
	OpBorrowSub              = op.OpBorrowSub
	OpAddWithCarry           = op.OpAddWithCarry
	OpSubWithBorrow          = op.OpSubWithBorrow
	OpVLoad                  = op.OpVLoad
	OpVStore                 = op.OpVStore
	OpVSplat                 = op.OpVSplat
	OpVConst                 = op.OpVConst
	OpVExtract               = op.OpVExtract
	OpVInsert                = op.OpVInsert
	OpVCompare               = op.OpVCompare
	OpVMaskBits              = op.OpVMaskBits
	OpVZero                  = op.OpVZero
	OpVInsertZero            = op.OpVInsertZero
	OpLeadingZeros           = op.OpLeadingZeros
	OpTrailingZeros          = op.OpTrailingZeros
	OpFProductError          = op.OpFProductError
	OpGet                    = op.OpGet
	OpSet                    = op.OpSet
	OpScopeEnter             = op.OpScopeEnter
	OpScopeLeave             = op.OpScopeLeave
	OpVCarrylessMul          = op.OpVCarrylessMul
	OpLoad16                 = op.OpLoad16
	OpStore16                = op.OpStore16
	OpLoad32                 = op.OpLoad32
	OpStore32                = op.OpStore32
	OpDiv                    = op.OpDiv
	OpDivSigned              = op.OpDivSigned
	OpRem                    = op.OpRem
	OpRemSigned              = op.OpRemSigned
	OpFFromSigned            = op.OpFFromSigned
	OpFFromUnsigned          = op.OpFFromUnsigned
	OpFToSigned              = op.OpFToSigned
	OpFToUnsigned            = op.OpFToUnsigned
	OpMulHighSigned          = op.OpMulHighSigned
	OpFCompare               = op.OpFCompare
)

// opcodeEnd is the exclusive upper bound of valid opcodes.
const opcodeEnd = OpFCompare + 1

// Instruction is a semantic operation. A, B, C, and Args refer to preceding
// instruction indices in the same Program: values defined earlier in the same
// block, or in a block that dominates this one (see docs/ir.md). Immediate values use their uint64
// bit pattern; Bits zero denotes the default 64-bit width. Memory addresses
// must be scalar 32- or 64-bit values, for every load and store width.
// Store8 accepts any valid scalar value width and stores its low byte.
// Store16 and Store32 accept a value of their access width or 64 bits and
// store its low 16 or 32 bits. Store64 accepts only a 64-bit value. Cast
// other widths explicitly before storing.
// ScopeEnter and ScopeLeave Args obey the same preceding-value and
// dominance rule as other instruction inputs.
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
	// EntryReg is assigned by lowering for label parameters. Source IR must
	// leave it at zero (or -1 on OpLabelParam); physical registers are compiler-owned.
	EntryReg int
	A        int
	B        int
	C        int
	Imm      uint64
	// ByteOrder applies to loads and stores. Native is the default; explicit
	// orders survive addressing-mode selection and register allocation.
	ByteOrder ByteOrder
	// Aligned promises natural alignment for OpLoad16/32 and OpStore16/32.
	// False permits arbitrary byte alignment; lowering supplies a safe fallback.
	Aligned bool
	// Contract permits a single-use FMul input of FAdd to be contracted to FMA.
	// It is set only for deferred expression trees, where reassociation is allowed.
	Contract bool
	Args     []int
	// Effect optionally replaces the preflight effect of OpCallC/OpCallGo. It
	// must be a function-call effect for the call's ABI that describes its
	// result and clobbers its result register, if it names one; lowering rejects
	// one that differs from the target's convention.
	Effect *abi.CallEffect
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
