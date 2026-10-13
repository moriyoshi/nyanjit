package i386

import (
	"math"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Policy describes the 32-bit x86 register and addressing constraints.
type Policy struct {
	abi   abi.Flavor
	stack abi.StackPractice
}

func NewPolicy(flavor abi.Flavor, stack abi.StackPractice) Policy {
	return Policy{abi: flavor, stack: stack}
}
func (Policy) Architecture() string    { return "386" }
func (p Policy) ABIFlavor() abi.Flavor { return p.abi }
func (Policy) RegisterBits() uint8     { return 32 }
func (Policy) GoCallMaxArguments() int { return 9 }
func (Policy) SupportsOrdinalSP() bool { return false }

// PhysicalRegisters returns an immutable value.
func (Policy) PhysicalRegisters() backend.RegisterSet {
	return policyRegisters0
}

// Registers returns an immutable value.
func (p Policy) Registers() backend.RegisterSet {
	if p.stack == abi.StackPlatform {
		return policyRegisters1
	}
	return policyRegisters2
}

// ParamRegister returns the ABI register for an integer parameter, or false
// for a stack argument or an index outside the ABI register bank. Compiler
// entry-arity limits are checked separately.
func (Policy) ParamRegister(int) (backend.Register, bool) { return 0, false }
func (Policy) ResultRegister() backend.Register           { return 0 }
func (Policy) ScratchRegister() backend.Register          { return 5 }
func (Policy) WorkspaceRegister() backend.Register        { return 5 }
func (Policy) CallTargetRegister() backend.Register       { return 0 }
func (Policy) LinkRegister() (backend.Register, bool)     { return 0, false }
func (p Policy) PreservesRegister(reg backend.Register) bool {
	return p.stack == abi.StackPlatform && (reg == 3 || reg == 5 || reg == 6 || reg == 7)
}
func (Policy) RegisterName(reg backend.Register) string { return registerNames[reg] }
func (Policy) CanScaled(scale, disp uint64) bool {
	if disp != 0 {
		return false
	}
	switch scale {
	case 1, 2, 4, 8:
		return true
	}
	return false
}
func (Policy) CanCombineScaled() bool { return true }
func (Policy) CanMulAdd() bool        { return false }
func (Policy) CanBinaryImmediate(operation op.Opcode, width uint8, _ uint64) bool {
	if width != 32 {
		return false
	}
	switch operation {
	case op.OpAdd, op.OpSub, op.OpAnd, op.OpOr, op.OpXor:
		return true
	}
	return false
}
func (Policy) CanFoldAddress(uint64, uint64) bool { return false }
func (Policy) CanFoldBaseOffset(uint64) bool      { return false }
func (Policy) CanReuse(ins machine.SelectedInstruction, position int) bool {
	switch ins.Op {
	case machine.SelectedVector:
		return position == 0 && ins.Arithmetic != op.OpVStore && ins.Arithmetic != op.OpVExtract && ins.Arithmetic != op.OpVMaskBits
	case machine.SelectedMove, machine.SelectedBinaryImmediate, machine.SelectedInc, machine.SelectedDec, machine.SelectedNarrow,
		machine.SelectedSignExtend, machine.SelectedShift, machine.SelectedMulHigh, machine.SelectedWordCarry, machine.SelectedBinary:
		return position == 0
	case machine.SelectedScaled, machine.SelectedLoadAddress,
		machine.SelectedLoad64, machine.SelectedLoad8, machine.SelectedLoadOffset:
		return true
	case machine.SelectedSelect:
		return position == 2
	case machine.SelectedDivide, machine.SelectedMulHighSigned:
		// The expansion reads both inputs before it writes the result.
		return position == 0 || position == 1
	}
	return false
}
func (Policy) BranchFits(_ machine.Instruction, sourcePC, targetPC, size int) bool {
	distance := int64(targetPC) - int64(sourcePC) - int64(size)
	return distance >= math.MinInt32 && distance <= math.MaxInt32
}

func (Policy) CanShiftBy(width uint8) bool {
	return width == 8 || width == 16 || width == 32 || width == 64
}

// SupportsFloat requires the target's mandatory SSE2 baseline.
func (Policy) SupportsFloat() bool    { return true }
func (Policy) SupportsFloatFMA() bool { return false }

var policyRegisters0 = backend.NewNamedRegisterSet(&registerNames, 0, 1, 2, 3, 4, 5, 6, 7)
var policyRegisters1 = backend.NewNamedRegisterSet(&registerNames, 0, 3, 1, 2, 6, 7)
var policyRegisters2 = backend.NewNamedRegisterSet(&registerNames, 0, 1, 2)
