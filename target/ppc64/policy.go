// Package ppc64 defines POWER register and instruction-selection constraints
// for big- and little-endian PPC64. The native backend supports integer and
// floating point code, FMA, calls and Linux syscalls.
package ppc64

import (
	"strconv"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// Policy reserves R11/R12 for expansion and call preparation, R29 for a
// caller-supplied workspace, and all runtime/platform fixed registers.
type Policy struct {
	profile abi.PPC64Profile
	stack   abi.StackPractice
	cpu     abi.PPC64CPU
}

// NewPolicy creates a POWER8 policy from an explicitly resolved ABI profile.
func NewPolicy(profile abi.PPC64Profile, stack abi.StackPractice) (Policy, error) {
	return NewPolicyForCPU(profile, stack, abi.PPC64Power8)
}

// NewPolicyForCPU selects instruction constraints for an explicit POWER level.
func NewPolicyForCPU(profile abi.PPC64Profile, stack abi.StackPractice, cpu abi.PPC64CPU) (Policy, error) {
	level, err := cpu.Resolve()
	if err != nil {
		return Policy{}, err
	}
	if _, err := profile.ClassifyEntry(0); err != nil {
		return Policy{}, err
	}
	if stack != abi.StackNoUse && stack != abi.StackPlatform {
		return Policy{}, abi.ErrInvalidSpec
	}
	return Policy{profile: profile, stack: stack, cpu: level}, nil
}

func (policy Policy) CPU() abi.PPC64CPU { return policy.cpu }

func (policy Policy) Architecture() string  { return policy.profile.Architecture() }
func (policy Policy) CABI() abi.PPC64CABI   { return policy.profile.CABI() }
func (policy Policy) ABIFlavor() abi.Flavor { return policy.profile.Flavor() }
func (Policy) RegisterBits() uint8          { return 64 }
func (Policy) GoCallMaxArguments() int      { return 12 }
func (Policy) SupportsOrdinalSP() bool      { return false }
func (policy Policy) NativeByteOrder() ir.ByteOrder {
	if policy.Architecture() == "ppc64le" {
		return ir.ByteOrderLittleEndian
	}
	return ir.ByteOrderBigEndian
}
func (Policy) PhysicalRegisters() []machine.Register {
	registers := make([]machine.Register, 32)
	for i := range registers {
		registers[i] = machine.Register(i)
	}
	return registers
}
func (policy Policy) Registers() []machine.Register {
	registers := []machine.Register{3, 4, 5, 6, 7, 8, 9, 10}
	if policy.ABIFlavor() == abi.Go {
		for register := machine.Register(14); register < 29; register++ {
			registers = append(registers, register)
		}
	}
	return registers
}
func (policy Policy) ParamRegister(index int) machine.Register {
	if policy.ABIFlavor() == abi.Go && index >= 8 {
		return machine.Register(14 + index - 8)
	}
	return machine.Register(3 + index)
}
func (Policy) ResultRegister() machine.Register     { return 3 }
func (Policy) ScratchRegister() machine.Register    { return 10 }
func (Policy) WorkspaceRegister() machine.Register  { return 29 }
func (Policy) CallTargetRegister() machine.Register { return 12 }
func (Policy) RegisterName(register machine.Register) string {
	return "R" + strconv.Itoa(int(register))
}
func (Policy) ReturnAddress() machine.ReturnAddress {
	return machine.ReturnAddress{Resource: machine.Resource{Class: machine.ResourceLinkRegister}}
}
func (policy Policy) Integer128Result() ([2]machine.Register, bool) {
	if policy.ABIFlavor() != abi.C {
		return [2]machine.Register{}, false
	}
	if policy.Architecture() == "ppc64" {
		return [2]machine.Register{4, 3}, true
	}
	return [2]machine.Register{3, 4}, true
}

// ManagedFrameLimit is supplied by the native compiler target, which also
// knows whether a C entry is configured for a Go-managed context.
func (Policy) ManagedFrameLimit() int { return 0 }
func (policy Policy) PreservesRegister(register machine.Register) bool {
	// R2 is fixed and never allocated. Calls and frames restore their saved
	// TOC; the transient call effect still conservatively lists it in preflight.
	return register == 2 || policy.ABIFlavor() == abi.C && register >= 14 && register <= 31
}
func (Policy) CanScaled(scale, displacement uint64) bool {
	return displacement == 0 && scale != 0 && scale&(scale-1) == 0
}
func (Policy) CanCombineScaled() bool        { return false }
func (policy Policy) CanMulAdd() bool        { return policy.cpu >= abi.PPC64Power9 }
func (Policy) CanWordCarry(width uint8) bool { return width == 64 }
func (Policy) CanMulHigh(width uint8) bool {
	return width == 8 || width == 16 || width == 32 || width == 64
}
func (Policy) CanShiftBy(width uint8) bool {
	return width == 8 || width == 16 || width == 32 || width == 64
}
func (Policy) SupportsFloatFMA() bool { return true }
func (Policy) CanBinaryImmediate(operation ir.Opcode, width uint8, value uint64) bool {
	if width != 8 && width != 16 && width != 32 && width != 64 {
		return false
	}
	switch operation {
	case ir.OpAdd:
		return int64(value) >= -32768 && int64(value) <= 32767
	case ir.OpSub:
		return int64(value) >= -32767 && int64(value) <= 32768
	case ir.OpAnd, ir.OpOr, ir.OpXor:
		return value <= 65535
	default:
		return false
	}
}
func (Policy) CanFoldAddress(scale, displacement uint64) bool {
	return scale == 1 && displacement == 0
}

// CanCompareImmediate permits a constant expansion in reserved R12 when a
// literal does not fit the compare instruction's immediate field.
func (Policy) CanCompareImmediate(width uint8, mode machine.ComparisonMode, _ uint64) bool {
	return (width == 32 || width == 64) && mode <= machine.CompareUnsigned
}
func (Policy) CanFoldBaseOffset(displacement uint64) bool {
	return int64(displacement) >= -32768 && int64(displacement) <= 32764 && displacement&3 == 0
}
func (Policy) CanReuse(ins machine.SelectedInstruction, position int) bool {
	switch ins.Op {
	case machine.SelectedMove, machine.SelectedBinaryImmediate, machine.SelectedInc,
		machine.SelectedDec, machine.SelectedNarrow, machine.SelectedSignExtend,
		machine.SelectedShift, machine.SelectedBinary:
		return position == 0
	case machine.SelectedFloatUnary, machine.SelectedFloatBinary, machine.SelectedFloatFMA:
		return true // All input bit patterns reach FPRs before any GPR result is written.
	case machine.SelectedCarryBit, machine.SelectedWordCarry, machine.SelectedMulHigh, machine.SelectedShiftBy, machine.SelectedMulAdd:
		return true // Every input is consumed before its register may be reused.
	case machine.SelectedSelect, machine.SelectedScaled, machine.SelectedLoad64, machine.SelectedLoad8,
		machine.SelectedLoadAddress, machine.SelectedLoadOffset:
		return true
	default:
		return false
	}
}

// BranchFits accounts for the instruction carrying the displacement in a
// conditional expansion. Far branches require a future explicit expansion.
func (policy Policy) BranchFits(ins machine.Instruction, sourcePC, targetPC, _ int) bool {
	distance := int64(targetPC) - int64(sourcePC)
	if ins.Op == machine.OpJumpIf || ins.Op == machine.OpLoop {
		if isCarryBranch(ins.Condition) {
			distance -= carryBranchPrefix(policy.cpu)
		}
		if !ins.Long {
			return distance&3 == 0 && distance >= -32768 && distance <= 32764
		}
		distance -= 4
	}
	return distance&3 == 0 && distance >= -(1<<25) && distance <= (1<<25)-4
}

// ResourceName gives report names without treating special registers as GPRs.
func (policy Policy) ResourceName(resource machine.Resource) string {
	switch resource.Class {
	case machine.ResourceGeneralRegister:
		return policy.RegisterName(machine.Register(resource.Number))
	case machine.ResourceLinkRegister:
		return "LR"
	case machine.ResourceCountRegister:
		return "CTR"
	case machine.ResourceConditionField:
		return "CR" + strconv.Itoa(int(resource.Number))
	case machine.ResourceIntegerStatus:
		return "XER"
	case machine.ResourceFloatingStatus:
		return "FPSCR"
	default:
		return ""
	}
}
