// Package machine holds selected virtual operations and the physical
// instruction stream consumed by architecture-specific assemblers. Physical
// register numbers denote whole storage units; instruction width selects an
// alias view only after allocation.
package machine

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// Register is a target-specific physical register ordinal.
type Register uint8

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
	// low and high product registers. The 386 backend uses EAX and EDX.
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
)

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
	VectorType ir.Type
	Op         Opcode
	Bits       uint8
	// FlagRequest asks the physical emitter to establish useful result facts.
	// It is a hint; the emitter reports the flags its expansion actually writes.
	FlagRequest   FlagMask
	PreserveFlags bool // Select a flag-neutral arithmetic form after flag liveness analysis.
	Symbol        string
	Arithmetic    ir.Opcode
	Condition     ir.BranchCondition
	Comparison    ComparisonMode
	ByteOrder     ir.ByteOrder
	PPC64CABI     abi.PPC64CABI // resolved callee convention for OpCallC
	A, B, C, D, E Register
	CarryMode     CarryMode
	Imm, Disp     uint64
	CallAreaBytes int // OpFrameEnter: outgoing argument/result area below spill slots.
	Slot          int
	Long          bool // long, flag-preserving expansion for limited-range branches
	Far           bool // far branch form, shortened before assembly when a nearer form fits
}

// RelocationEncoding selects the fixed-size symbol load to patch.
type RelocationEncoding uint8

const (
	RelocAMD64Immediate RelocationEncoding = iota
	RelocARM64WideMoves
	Reloc386Immediate
	RelocRISCV64ByteSequence
	RelocPPC64BigEndian
	RelocPPC64LittleEndian
)

// Relocation marks a symbol load in an assembled byte stream.
type Relocation struct {
	Symbol   string
	Offset   int
	Encoding RelocationEncoding
}
