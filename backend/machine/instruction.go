// Package machine holds selected virtual operations and the physical
// instruction stream consumed by architecture-specific assemblers. Physical
// register numbers denote whole storage units; instruction width selects an
// alias view only after allocation. It also lays out the stream: branch
// relaxation, literal pools, relocations and Layout, which maps PCs back to
// instructions.
//
// Construct this package's exported structs with keyed fields. Field order is
// not part of the API: fields may be reordered or packed, though removing a
// field or changing its meaning is a breaking change.
package machine

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Opcode identifies one allocated operation.
type Opcode uint8

const (
	OpConst Opcode = iota
	OpSymbol
	OpMove
	OpBinary
	OpBinaryImmediate
	OpBinaryCarry
	OpInc
	OpDec
	OpNarrow
	OpSignExtend
	OpShift
	OpScaled
	OpMulAdd
	OpSelect
	OpSelectFlags
	OpLoad64
	OpStore64
	OpLoad8
	OpStore8
	OpLoadAddress
	OpStoreAddress
	OpLoadOffset
	OpStoreOffset
	OpSyscall
	OpCallC
	OpCallGo
	OpSaveLink
	OpRestoreLink
	OpSpill
	OpReload
	OpLoadIncoming
	OpFrameEnter
	OpFrameLeave
	OpReturn
	OpCompareImmediate
	OpCompareRegister
	OpJump
	OpJumpIf
	OpBranchZero
	OpBranchBit
	OpLoop
	// OpMulWide multiplies the implicit accumulator by A into the target's
	// low and high product registers. Only 386 has one (EAX, into EDX:EAX);
	// Visit rejects it on other targets.
	OpMulWide
	// OpSaveCFloat and OpRestoreCFloat preserve the low 64 bits of an
	// arm64 C ABI callee-saved vector register across Go ABI calls.
	OpSaveCFloat
	OpRestoreCFloat
	// OpFloatBinary transfers integer bit patterns through volatile FP registers.
	OpSaveCVector
	OpRestoreCVector
	OpSaveCondition
	OpRestoreCondition
	OpFloatBinary
	OpFloatFMA
	OpFloatUnary
	// OpSetCount initializes the architecture's dedicated loop counter from A.
	OpSetCount
	// OpShiftBy writes A from B using unsigned count C. Arithmetic names the
	// corresponding immediate shift/rotate; Bits is the semantic value width.
	OpShiftBy
	// OpMulHigh writes the high Bits of unsigned B*C to A.
	OpMulHigh
	OpCarryBit
	OpWordCarry
	OpVector
	OpVectorMove
	OpVectorSpill
	OpVectorReload
	OpCountZeros
	OpAndNot
	OpFloatMove
	OpLiteralLoad
	OpLiteralData
	OpFloatSpill
	OpFloatReload
	OpFloatBits
	// Narrow memory: A/B are dst/address for loads and address/value for stores.
	// The opcode fixes the access width; loads zero-extend and stores truncate.
	OpLoad16
	OpStore16
	OpLoad32
	OpStore32
	// OpBarrier emits the target's barrier for Instruction.Barrier. It reads
	// and writes no registers or flags; see Barrier for the guarantees.
	OpBarrier
	// OpDivide writes to A the quotient or remainder (Arithmetic: op.OpDiv,
	// OpDivSigned, OpRem or OpRemSigned) of dividend B by divisor C, Bits
	// wide, with the op.DivisionSemantics in Imm. B and C hold zero-extended
	// Bits-wide values, and so does the result.
	OpDivide
	// OpIntToFloat writes to FP register A the binary32 or binary64 (Bits)
	// conversion of the integer in GP register B (with HighB its high word
	// on a 32-bit target), signed or unsigned by Arithmetic (op.OpFFromSigned
	// or OpFFromUnsigned); Imm is the integer's width, 32 or 64.
	OpIntToFloat
	// OpFloatToInt writes to GP register A (with HighA the high word on a
	// 32-bit target) the conversion of the float in FP register B, truncated
	// toward zero to a Bits-wide integer, signed or unsigned by Arithmetic
	// (op.OpFToSigned or OpFToUnsigned), with the op.ConversionSemantics in
	// Imm; Disp is the float's width, 32 or 64.
	OpFloatToInt
	// OpMulHighSigned writes the high Bits of signed B*C to A.
	OpMulHighSigned
	// OpFloatCompare writes to GP register A 1 when the Bits-wide floats in
	// FP registers B and C satisfy the op.FloatCondition in Imm, else 0.
	OpFloatCompare
	opcodeEnd // Keep last: the exclusive upper bound of recognized opcodes.
)

// Valid reports whether op identifies a recognized allocated operation.
func (op Opcode) Valid() bool { return op < opcodeEnd }

// CarryMode selects a word carry input. CarryOperands computes carry/borrow
// from low operands D/E. CarryFlags consumes a proven incoming XER fact.
type CarryMode uint8

const (
	CarryValue CarryMode = iota
	CarryBitValue
	CarryOperands
	CarryFlags
)

// Instruction is an allocated operation awaiting architecture assembly.
// A, B, C, D, and E hold physical registers according to Op; Imm and Disp are
// scalar operands, and Slot names an allocator spill slot.
type Instruction struct {
	Data       []byte // immutable literal bytes; Disp receives the final pool PC
	VectorType abi.ValueType
	Op         Opcode
	Bits       uint8
	// FlagRequest asks the physical emitter to establish useful result facts.
	// It is a hint; the emitter reports the flags its expansion actually writes.
	FlagRequest   FlagMask
	PreserveFlags bool // Select a flag-neutral arithmetic form after flag liveness analysis.
	Symbol        string
	Arithmetic    op.Opcode
	Condition     op.BranchCondition
	Comparison    ComparisonMode
	ByteOrder     op.ByteOrder
	Barrier       Barrier // OpBarrier: the guarantee required
	A, B, C, D, E backend.Register
	// HighA/HighB/HighC hold second GP words for 32-bit-target FP transfers,
	// paired shifts and wide symbol loads.
	// Binary32 and FP-register operands ignore their corresponding high word.
	HighA, HighB, HighC backend.Register
	CarryMode           CarryMode
	Imm, Disp           uint64
	CallAreaBytes       int // OpFrameEnter: outgoing argument/result area below spill slots.
	Slot                int
	Indexed             bool // OpVector memory: C is an unscaled GP index.
	Long                bool // long, flag-preserving expansion for limited-range branches
	Far                 bool // far branch form, shortened before assembly when a nearer form fits
}

// RelocationEncoding selects the fixed-size symbol or local-address load to patch.
type RelocationEncoding uint8

const (
	RelocAMD64Immediate RelocationEncoding = iota
	RelocARM64WideMoves
	Reloc386Immediate
	RelocRISCV64ByteSequence
	RelocPPC64LittleEndian
	// Reloc386WideImmediate patches consecutive low/high MOV imm32 loads.
	Reloc386WideImmediate
	// Reloc386Literal patches an absolute SSE2 pool load with code base + TargetOffset.
	Reloc386Literal
	// RelocLoong64Constant patches lu12i.w/ori/lu32i.d/lu52i.d.
	RelocLoong64Constant
)

// Relocation marks a symbol or code-local address load in an assembled byte stream.
type Relocation struct {
	TargetOffset uint64 // local byte offset for code-base relocations; ignored for symbols
	Symbol       string
	Offset       int
	Encoding     RelocationEncoding
}

// NeedsCodeBase reports whether the relocation refers to this code object's
// final mapping rather than an externally bound symbol.
func (r Relocation) NeedsCodeBase() bool { return r.Encoding == Reloc386Literal }
