package machine

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/regalloc"
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
)

// SelectedInstruction refers to virtual value IDs. The allocator replaces
// those IDs with physical registers and spill locations before assembly.
type SelectedInstruction struct {
	Variable    int
	Data        []byte
	LiteralMask uint8 // FP operands supplied by literal loads, excluded from Inputs
	Type        ir.Type
	Op          SelectedOpcode
	Bits        uint8
	ByteOrder   ir.ByteOrder
	// ZeroExtended says this immediate operation clears bits above Bits.
	ZeroExtended bool
	Symbol       string
	Arithmetic   ir.Opcode
	Dst          int
	A, B, C      int
	Imm, Disp    uint64
	Args         []int
	Effect       *abi.CallEffect
}

// Inputs reports virtual values read by this operation in operand order.
func (ins SelectedInstruction) Inputs() []int {
	if ins.Op == SelectedFloatUnary || ins.Op == SelectedFloatBinary || ins.Op == SelectedFloatFMA {
		ids := []int{ins.A}
		if ins.Op != SelectedFloatUnary {
			ids = append(ids, ins.B)
		}
		if ins.Op == SelectedFloatFMA {
			ids = append(ids, ins.C)
		}
		var out []int
		for n, id := range ids {
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
		return []int{ins.A}
	case SelectedHomeBoundary:
		return ins.Args
	case SelectedVector:
		return (ir.Instruction{Op: ins.Arithmetic, A: ins.A, B: ins.B, C: ins.C}).Inputs()
	case SelectedWordCarry:
		if len(ins.Args) != 0 {
			return ins.Args
		}
		return []int{ins.A, ins.B, ins.C}
	case SelectedFloatMove, SelectedFloatBits, SelectedCountZeros, SelectedFloatUnary, SelectedMove, SelectedBinaryImmediate, SelectedInc, SelectedDec, SelectedNarrow, SelectedSignExtend,
		SelectedShift, SelectedLoad64, SelectedLoad8, SelectedLoadOffset:
		return []int{ins.A}
	case SelectedAndNot, SelectedCarryBit, SelectedMulHigh, SelectedShiftBy, SelectedBinary, SelectedFloatBinary, SelectedScaled, SelectedStore64, SelectedStore8,
		SelectedLoadAddress, SelectedStoreOffset:
		return []int{ins.A, ins.B}
	case SelectedMulAdd, SelectedSelect, SelectedFloatFMA, SelectedStoreAddress:
		return []int{ins.A, ins.B, ins.C}
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
	Class     regalloc.Class
	Words     uint8 // zero means one register word
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
	PreferredRegisters map[int]Register // advisory value homes before allocation
	Result             int
	LoopPressure       bool // Permit preserved registers when a loop would otherwise spill.
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
// reserved, even when Register is used. Registers hold one native word.
type Home struct {
	Slot     int
	Register int
}

// HomePlan is shared by all fragments of a function; edges never transfer it.
type HomePlan struct {
	Slots               []int
	FloatHomes          []bool
	AdditionalRegisters []Register
	Regions             map[int]map[int]Home
	Modified            map[int]map[int]bool
	Parents             map[int]int
	Captures            map[int]bool
	Reserved            map[Register]bool
	Words               []int
	EntrySlots          []int
	ReservedSlots       int
}
