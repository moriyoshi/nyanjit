// Package ppc64 defines POWER register and instruction-selection constraints
// for little-endian PPC64 (ppc64le, ELFv2). The native backend supports integer and
// floating point code, FMA, calls and Linux syscalls.
//
// The generic encoder path: build machine.Instruction values, expand each with
// Emitter.Expand at its PC, and encode the result with ByteAssembler or
// TextAssembler. FORMS.md lists the accepted instruction forms, their operand
// constraints and sizes (Emitter.Size); a table test checks every row.
// GoFrame describes a laid-out function to the Go unwinder for goregister.
//
// Construct this package's exported structs with keyed fields. Field order is
// not part of the API: fields may be reordered or packed, though removing a
// field or changing its meaning is a breaking change.
package ppc64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
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

func (policy Policy) Architecture() string   { return policy.profile.Architecture() }
func (policy Policy) ABIFlavor() abi.Flavor  { return policy.profile.Flavor() }
func (Policy) RegisterBits() uint8           { return 64 }
func (Policy) GoCallMaxArguments() int       { return 12 }
func (Policy) SupportsOrdinalSP() bool       { return false }
func (Policy) NativeByteOrder() op.ByteOrder { return op.ByteOrderLittleEndian }

// PhysicalRegisters returns an immutable value.
func (Policy) PhysicalRegisters() backend.RegisterSet { return physicalRegisters }

// Registers returns an immutable value.
func (policy Policy) Registers() backend.RegisterSet {
	if policy.ABIFlavor() == abi.Go {
		return goRegisters
	}
	return cRegisters
}

// ParamRegister returns the ABI register for an integer parameter, or false
// for a stack argument or an index outside the ABI register bank. Compiler
// entry-arity limits are checked separately.
func (policy Policy) ParamRegister(index int) (backend.Register, bool) {
	if index < 0 {
		return 0, false
	}
	if policy.ABIFlavor() == abi.Go && index >= 8 && index < 12 {
		return backend.Register(14 + index - 8), true
	}
	if index >= 8 {
		return 0, false
	}
	return backend.Register(3 + index), true
}
func (Policy) ResultRegister() backend.Register     { return 3 }
func (Policy) ScratchRegister() backend.Register    { return 10 }
func (Policy) WorkspaceRegister() backend.Register  { return 29 }
func (Policy) CallTargetRegister() backend.Register { return 12 }
func (Policy) RegisterName(register backend.Register) string {
	return registerNames[register]
}
func (Policy) ReturnAddress() machine.ReturnAddress {
	return machine.ReturnAddress{Resource: machine.Resource{Class: machine.ResourceLinkRegister}}
}
func (policy Policy) Integer128Result() ([2]backend.Register, bool) {
	if policy.ABIFlavor() != abi.C {
		return [2]backend.Register{}, false
	}
	return [2]backend.Register{3, 4}, true
}

// ManagedFrameLimit is supplied by the native compiler target, which also
// knows whether a C entry is configured for a Go-managed context.
func (Policy) ManagedFrameLimit() int { return 0 }
func (policy Policy) PreservesRegister(register backend.Register) bool {
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
func (Policy) CanMulHighSigned(width uint8) bool {
	return width == 8 || width == 16 || width == 32 || width == 64
}
func (Policy) CanShiftBy(width uint8) bool {
	return width == 8 || width == 16 || width == 32 || width == 64
}
func (Policy) SupportsFloatFMA() bool { return true }
func (Policy) CanBinaryImmediate(operation op.Opcode, width uint8, value uint64) bool {
	if width != 8 && width != 16 && width != 32 && width != 64 {
		return false
	}
	switch operation {
	case op.OpAdd:
		return int64(value) >= -32768 && int64(value) <= 32767
	case op.OpSub:
		return int64(value) >= -32767 && int64(value) <= 32768
	case op.OpAnd, op.OpOr, op.OpXor:
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
	case machine.SelectedCarryBit, machine.SelectedWordCarry, machine.SelectedMulHigh, machine.SelectedMulHighSigned, machine.SelectedShiftBy, machine.SelectedMulAdd,
		machine.SelectedDivide:
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
		return policy.RegisterName(backend.Register(resource.Number))
	case machine.ResourceLinkRegister:
		return "LR"
	case machine.ResourceCountRegister:
		return "CTR"
	case machine.ResourceConditionField:
		return registerNamesCR[resource.Number]
	case machine.ResourceIntegerStatus:
		return "XER"
	case machine.ResourceFloatingStatus:
		return "FPSCR"
	default:
		return ""
	}
}

var physicalRegisters = backend.NewNamedRegisterSet(&registerNames, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31)

var goRegisters = backend.NewNamedRegisterSet(&registerNames, 3, 4, 5, 6, 7, 8, 9, 10, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28)
var cRegisters = backend.NewNamedRegisterSet(&registerNames, 3, 4, 5, 6, 7, 8, 9, 10)
