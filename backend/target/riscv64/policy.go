package riscv64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Policy describes the RV64 integer register and addressing constraints.
// X30 and X31 are reserved for instruction expansion and call targets.
type Policy struct {
	flavor abi.Flavor
	stack  abi.StackPractice
}

func NewPolicy(flavor abi.Flavor, stack abi.StackPractice) Policy {
	return Policy{flavor: flavor, stack: stack}
}

func (Policy) Architecture() string    { return "riscv64" }
func (p Policy) ABIFlavor() abi.Flavor { return p.flavor }
func (Policy) RegisterBits() uint8     { return 64 }
func (Policy) GoCallMaxArguments() int { return 9 }
func (Policy) SupportsOrdinalSP() bool { return false }

// PhysicalRegisters returns an immutable value.
func (Policy) PhysicalRegisters() backend.RegisterSet { return physicalRegisters }

// Registers returns an immutable value.
func (Policy) Registers() backend.RegisterSet {
	return policyRegisters0
}

// ParamRegister returns the ABI register for an integer parameter, or false
// for a stack argument or an index outside the ABI register bank. Compiler
// entry-arity limits are checked separately.
func (p Policy) ParamRegister(index int) (backend.Register, bool) {
	if p.flavor == abi.Go && index >= 8 && index < 16 {
		// X9 is also the workspace register; entry limits are checked separately.
		registers := [...]backend.Register{8, 9, 18, 19, 20, 21, 22, 23}
		return registers[index-8], true
	}
	if index < 0 || index >= 8 {
		return 0, false
	}
	return backend.Register(10 + index), true
}
func (Policy) ResultRegister() backend.Register         { return 10 }
func (Policy) ScratchRegister() backend.Register        { return 29 }
func (Policy) WorkspaceRegister() backend.Register      { return 9 }
func (Policy) RegisterName(reg backend.Register) string { return registerNames[reg] }
func (Policy) CanScaled(scale, disp uint64) bool {
	return disp == 0 && scale != 0 && scale&(scale-1) == 0
}
func (Policy) CanCombineScaled() bool { return false }
func (Policy) CanMulAdd() bool        { return true }
func (Policy) SupportsFloatFMA() bool { return true }
func (Policy) CanBinaryImmediate(operation op.Opcode, width uint8, value uint64) bool {
	if width != 8 && width != 16 && width != 32 && width != 64 {
		return false
	}
	immediate := int64(value)
	switch operation {
	case op.OpAdd, op.OpAnd, op.OpOr, op.OpXor:
		return immediate >= -2048 && immediate <= 2047
	case op.OpSub:
		return immediate >= -2047 && immediate <= 2048
	default:
		return false
	}
}
func (Policy) CanFoldAddress(scale, _ uint64) bool {
	return scale != 0
}
func (Policy) CanFoldBaseOffset(uint64) bool          { return true }
func (Policy) CallTargetRegister() backend.Register   { return 31 }
func (Policy) LinkRegister() (backend.Register, bool) { return 1, true }
func (p Policy) PreservesRegister(reg backend.Register) bool {
	if p.flavor == abi.C {
		return reg == 8 || reg == 9 || reg >= 18 && reg <= 27
	}
	// Go may write s-register allocation homes; only architectural/runtime
	// reserved registers (zero, SP, GP, TP, g) retain their values.
	return reg == 0 || reg == 2 || reg == 3 || reg == 4 || reg == 27
}
func (Policy) CanReuse(ins machine.SelectedInstruction, position int) bool {
	switch ins.Op {
	case machine.SelectedFloatUnary, machine.SelectedMove, machine.SelectedBinaryImmediate, machine.SelectedDec, machine.SelectedNarrow,
		machine.SelectedSignExtend, machine.SelectedShift, machine.SelectedBinary:
		return position == 0
	case machine.SelectedScaled, machine.SelectedMulAdd, machine.SelectedLoad64,
		machine.SelectedLoad8, machine.SelectedLoadAddress, machine.SelectedLoadOffset:
		return true
	case machine.SelectedDivide, machine.SelectedMulHighSigned:
		// The expansion reads both inputs (or copies them to X30/X31)
		// before its first write of the destination.
		return true
	case machine.SelectedSelect:
		return position >= 0 && position < 3
	}
	return false
}

func (Policy) BranchFits(ins machine.Instruction, sourcePC, targetPC, _ int) bool {
	distance := int64(targetPC) - int64(sourcePC)
	if ins.Op == machine.OpJumpIf || ins.Op == machine.OpBranchZero {
		if !ins.Long {
			return distance%2 == 0 && distance >= -(1<<12) && distance < 1<<12
		}
		distance -= 4 // The jump follows the inverted conditional branch.
	}
	if ins.Long && (ins.Op == machine.OpJump || (ins.Op == machine.OpJumpIf || ins.Op == machine.OpBranchZero) && ins.Far) {
		// AUIPC supplies a signed 20-bit upper displacement. JALR adds a
		// signed 12-bit lower displacement, rounded into that upper part.
		upper := (distance + 2048) >> 12
		return distance%2 == 0 && upper >= -(1<<19) && upper < 1<<19
	}
	return distance%2 == 0 && distance >= -(1<<20) && distance < 1<<20
}

var physicalRegisters = backend.NewNamedRegisterSet(&registerNames, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31)

var policyRegisters0 = backend.NewNamedRegisterSet(&registerNames, 10, 11, 12, 13, 14, 15, 16, 17, 5, 6, 7, 28, 29)
