package machine

import (
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// SelectedOpcode identifies an operation after target instruction selection
// and before physical register allocation.
type SelectedOpcode uint8

const (
	SelectedConst SelectedOpcode = iota
	SelectedSymbol
	SelectedMove
	SelectedBinary
	SelectedBinaryImmediate
	SelectedInc
	SelectedDec
	SelectedNarrow
	SelectedSignExtend
	SelectedShift
	SelectedScaled
	SelectedMulAdd
	SelectedSelect
	SelectedLoad64
	SelectedStore64
	SelectedLoad8
	SelectedStore8
	SelectedLoadAddress
	SelectedStoreAddress
	SelectedLoadOffset
	SelectedStoreOffset
	SelectedSyscall
	SelectedCallC
	SelectedCallGo
	SelectedFloatBinary
	SelectedFloatFMA
	SelectedFloatUnary
	// SelectedShiftBy retains the full unsigned count B for value A.
	SelectedShiftBy
	SelectedMulHigh
	SelectedCarryBit
	// SelectedWordCarry takes A/B/C, or Args=[highA,highB,lowA,lowB].
	SelectedWordCarry
	SelectedVector
	SelectedCountZeros
	SelectedAndNot
	SelectedFloatMove
	SelectedFloatBits
	SelectedHomeGet
	SelectedHomeSet
	SelectedHomeBoundary
	SelectedLoad16
	SelectedStore16
	SelectedLoad32
	SelectedStore32
	// SelectedDivide writes A divided by B (Arithmetic: op.OpDiv,
	// OpDivSigned, OpRem or OpRemSigned) to Dst, with the
	// op.DivisionSemantics in Imm.
	SelectedDivide
	// SelectedIntToFloat converts integer A to a binary32 or binary64 (Bits)
	// Dst; Arithmetic is op.OpFFromSigned or OpFFromUnsigned.
	SelectedIntToFloat
	// SelectedFloatToInt truncates float A to a Bits-wide integer Dst;
	// Arithmetic is op.OpFToSigned or OpFToUnsigned, and Imm holds the
	// op.ConversionSemantics.
	SelectedFloatToInt
	// SelectedMulHighSigned writes the high Bits of signed A*B to Dst.
	SelectedMulHighSigned
	// SelectedFloatCompare writes to integer Dst 1 when Bits-wide floats A
	// and B satisfy the op.FloatCondition in Imm, else 0.
	SelectedFloatCompare
)

// SelectedInstruction refers to virtual value IDs. The allocator replaces
// those IDs with physical registers and spill locations before assembly.
type SelectedInstruction struct {
	Variable    int
	Data        []byte
	LiteralMask uint8 // FP operands supplied by literal loads, excluded from Inputs
	Type        abi.ValueType
	Op          SelectedOpcode
	Bits        uint8
	ByteOrder   op.ByteOrder
	// ZeroExtended says this immediate operation clears bits above Bits.
	ZeroExtended bool
	Indexed      bool // SelectedVector memory: final input is a GP index.
	Symbol       string
	Arithmetic   op.Opcode
	Dst          int
	A, B, C      int
	Imm, Disp    uint64
	Args         []int
	Effect       *abi.CallEffect
}

// Inputs returns an independently mutable operand list in operand order.
func (ins SelectedInstruction) Inputs() []int {
	out := ins.InputsInto(nil)
	// Explicit arguments may be borrowed, including an empty slice with spare
	// capacity. Copy whenever Args is present so new borrowing cases stay safe.
	if ins.Args != nil {
		return slices.Clone(out)
	}
	return out
}

// InputsInto appends short operand lists into caller-owned scratch storage.
// Calls and explicit argument lists return their immutable backing slice.
func (ins SelectedInstruction) InputsInto(dst []int) []int {
	if ins.Op == SelectedFloatUnary || ins.Op == SelectedFloatBinary || ins.Op == SelectedFloatFMA {
		ids := [3]int{ins.A, ins.B, ins.C}
		count := 1
		if ins.Op != SelectedFloatUnary {
			count++
		}
		if ins.Op == SelectedFloatFMA {
			count++
		}
		out := dst
		for n, id := range ids[:count] {
			if ins.LiteralMask&(1<<n) == 0 {
				out = append(out, id)
			}
		}
		return out
	}
	switch ins.Op {
	case SelectedHomeGet:
		return nil
	case SelectedHomeSet:
		return append(dst, ins.A)
	case SelectedHomeBoundary:
		return ins.Args
	case SelectedVector:
		if ins.Args != nil {
			return ins.Args
		}
		return vectorInputs(ins.Arithmetic, ins.A, ins.B, ins.C, dst)
	case SelectedWordCarry:
		if len(ins.Args) != 0 {
			return ins.Args
		}
		return append(dst, ins.A, ins.B, ins.C)
	case SelectedFloatMove, SelectedFloatBits, SelectedCountZeros, SelectedFloatUnary, SelectedIntToFloat, SelectedFloatToInt, SelectedMove, SelectedBinaryImmediate, SelectedInc, SelectedDec, SelectedNarrow, SelectedSignExtend,
		SelectedShift, SelectedLoad64, SelectedLoad8, SelectedLoad16, SelectedLoad32, SelectedLoadOffset:
		return append(dst, ins.A)
	case SelectedAndNot, SelectedCarryBit, SelectedMulHigh, SelectedMulHighSigned, SelectedFloatCompare, SelectedDivide, SelectedShiftBy, SelectedBinary, SelectedFloatBinary, SelectedScaled, SelectedStore64, SelectedStore8, SelectedStore16, SelectedStore32,
		SelectedLoadAddress, SelectedStoreOffset:
		return append(dst, ins.A, ins.B)
	case SelectedMulAdd, SelectedSelect, SelectedFloatFMA, SelectedStoreAddress:
		return append(dst, ins.A, ins.B, ins.C)
	case SelectedSyscall, SelectedCallC, SelectedCallGo:
		return ins.Args
	default:
		return nil
	}
}

// SelectedValue records a virtual value's word count and initial home.
// Param, EntrySlot, and EntryReg are -1 unless that entry kind applies.
type SelectedValue struct {
	Bits      uint8 // logical scalar width; zero retains the legacy 64-bit default
	Class     backend.Class
	Words     uint8 // GP/ABI word count; Float and Vector residency uses one register
	Constant  bool
	Symbol    string
	Imm       uint64
	Param     int
	EntrySlot int
	EntryReg  int
}

// SelectedProgram is the target-shaped input to register allocation.
type SelectedProgram struct {
	Instructions       []SelectedInstruction
	HomePlan           *HomePlan
	HomeRegion         int
	Values             []SelectedValue
	Scopes             []SelectedScope
	PreferredRegisters map[int]backend.Register // advisory value homes before allocation
	Result             int
	ResultAsFloat      bool
	DiscardResult      bool // control jumps without arguments have no value result
	// ResultParts contains both logical 64-bit result IDs for a wide return.
	ResultParts     []int
	Exports         []int
	ExportRegisters []int
	DirectExports   bool
	ReservedSlots   int
}

// SelectedScope bounds the selected instructions emitted within one Builder
// scope. Parent is the enclosing scope ID; scope zero is the root.
type SelectedScope struct {
	ID, Parent int
	Start, End int
}

// Home is one fixed variable location inside a lexical region. Slot is always
// reserved, even when Register is used. FP registers can hold two GP words.
type Home struct {
	Bits     uint8 // scalar spill width; zero denotes the legacy 64-bit width
	Slot     int
	Register int
}

// HomePlan is shared by all fragments of a function; edges never transfer it.
type HomePlan struct {
	Slots               []int
	FloatHomes          []bool
	AdditionalRegisters []backend.Register
	// Regions contains the complete visible register and spill homes per region.
	Regions       map[int]map[int]Home
	Modified      map[int]map[int]bool
	Parents       map[int]int
	Captures      map[int]bool
	Reserved      map[backend.Register]bool
	Words         []int
	EntrySlots    []int
	ReservedSlots int

	HasFloatingHomes  bool
	VariableRegions   []int
	VariableBits      []uint8
	RegionBounds      map[int][2]int
	ReservedRegions   map[int][4]uint64
	RegisterVariables map[int][]int
	// RegisterOverrides is an optional compact alternative to Regions. It stores
	// register homes only and is used when Regions is nil. Lookup derives spill homes from Slots,
	// VariableRegions, VariableBits and RegionBounds. Compiler-private plans use
	// this form with Regions nil, avoiding a full map copy at every scope.
	RegisterOverrides map[int]map[int]Home
}

// vectorInputs lists the operands of a vector operation without explicit
// arguments, in the order the IR gives them.
func vectorInputs(operation op.Opcode, a, b, c int, dst []int) []int {
	switch operation {
	case op.OpGet, op.OpParam, op.OpLabelParam, op.OpConst, op.OpVConst, op.OpVZero, op.OpScopeEnter, op.OpScopeLeave, op.OpSyscall, op.OpCallC, op.OpCallGo:
		return nil
	case op.OpAdd, op.OpSub, op.OpMul, op.OpAnd, op.OpOr, op.OpXor, op.OpAddScaled, op.OpFAdd, op.OpFSub, op.OpFMul, op.OpFDiv, op.OpMulHigh, op.OpMulHighSigned, op.OpCarryAdd, op.OpBorrowSub, op.OpShiftLeftBy, op.OpShiftRightBy, op.OpShiftRightArithmeticBy, op.OpRotateLeftBy, op.OpRotateRightBy, op.OpStore64, op.OpStore8, op.OpStore16, op.OpStore32, op.OpVStore, op.OpVInsert, op.OpVInsertZero, op.OpVCompare, op.OpVCarrylessMul:
		return append(dst, a, b)
	case op.OpSelect, op.OpFMA, op.OpFProductError, op.OpAddWithCarry, op.OpSubWithBorrow:
		return append(dst, a, b, c)
	default:
		return append(dst, a)
	}
}
