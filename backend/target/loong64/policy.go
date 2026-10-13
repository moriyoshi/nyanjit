// Package loong64 implements baseline LA64 instruction selection and assembly.
//
// The generic encoder path: build machine.Instruction values and visit them
// with machine.Visit and a ByteAssembler or TextAssembler. FORMS.md lists the
// accepted instruction forms, their operand constraints and sizes
// (PhysicalSize); a table test checks every row. GoFrame describes a laid-out
// function to the Go unwinder for goregister.
//
// Construct this package's exported structs with keyed fields. Field order is
// not part of the API: fields may be reordered or packed, though removing a
// field or changing its meaning is a breaking change.
package loong64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Policy reserves R12/R19/R20 for expansions, R23 for the workspace, and
// the Go runtime's R22 (g), R29 (closure), and R30 (assembler temporary).
// R0, R1, R2, R3 and R21 retain their architectural/ABI roles.
type Policy struct {
	flavor abi.Flavor
	stack  abi.StackPractice
}

func NewPolicy(f abi.Flavor, s abi.StackPractice) Policy { return Policy{f, s} }
func (Policy) Architecture() string                      { return "loong64" }
func (p Policy) ABIFlavor() abi.Flavor                   { return p.flavor }
func (Policy) RegisterBits() uint8                       { return 64 }
func (Policy) GoCallMaxArguments() int                   { return 16 }
func (Policy) SupportsOrdinalSP() bool                   { return false }

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
	count := 8
	if p.flavor == abi.Go {
		count = 16
	}
	if index < 0 || index >= count {
		return 0, false
	}
	return backend.Register(4 + index), true
}
func (Policy) ResultRegister() backend.Register    { return 4 }
func (Policy) ScratchRegister() backend.Register   { return 18 }
func (Policy) WorkspaceRegister() backend.Register { return 23 }
func (Policy) RegisterName(r backend.Register) string {
	return registerNames[r]
}
func (Policy) CanScaled(s, d uint64) bool { return d == 0 && s != 0 && s&(s-1) == 0 }
func (Policy) CanCombineScaled() bool     { return false }
func (Policy) CanMulAdd() bool            { return true }
func (Policy) SupportsFloatFMA() bool     { return true }
func (Policy) CanBinaryImmediate(operation op.Opcode, w uint8, v uint64) bool {
	if w != 8 && w != 16 && w != 32 && w != 64 {
		return false
	}
	switch operation {
	case op.OpAdd:
		return int64(v) >= -2048 && int64(v) <= 2047
	case op.OpSub:
		return int64(v) >= -2047 && int64(v) <= 2048
	case op.OpAnd, op.OpOr, op.OpXor:
		return v <= 4095
	}
	return false
}
func (Policy) CanFoldAddress(s, d uint64) bool        { return s != 0 }
func (Policy) CanFoldBaseOffset(uint64) bool          { return true }
func (Policy) CallTargetRegister() backend.Register   { return 20 }
func (Policy) LinkRegister() (backend.Register, bool) { return 1, true }
func (p Policy) PreservesRegister(r backend.Register) bool {
	if p.flavor == abi.C {
		return r >= 22 && r < 32
	}
	// Go has no callee-saved allocation homes. Zero, TP, SP and g retain
	// their reserved roles; closure/assembler temporaries remain clobberable.
	return r == 0 || r == 2 || r == 3 || r == 22
}
func (Policy) CanReuse(i machine.SelectedInstruction, p int) bool {
	switch i.Op {
	case machine.SelectedVector:
		return p == 0 && i.Arithmetic != op.OpVLoad && i.Arithmetic != op.OpVStore && i.Arithmetic != op.OpVSplat && i.Arithmetic != op.OpVExtract && i.Arithmetic != op.OpVMaskBits
	case machine.SelectedMove, machine.SelectedBinaryImmediate, machine.SelectedDec, machine.SelectedNarrow, machine.SelectedSignExtend, machine.SelectedShift, machine.SelectedBinary, machine.SelectedShiftBy, machine.SelectedMulHigh, machine.SelectedMulHighSigned, machine.SelectedCountZeros:
		return p == 0
	case machine.SelectedScaled, machine.SelectedMulAdd, machine.SelectedLoad64, machine.SelectedLoad8, machine.SelectedLoadAddress, machine.SelectedLoadOffset:
		return true
	case machine.SelectedSelect:
		return p >= 0 && p < 3
	case machine.SelectedDivide:
		// Every expansion reads both inputs before it writes A.
		return p == 0 || p == 1
	case machine.SelectedFloatCompare:
		// The inputs are FP registers and the result a GP register.
		return false
	}
	return false
}
func (Policy) ReturnAddress() machine.ReturnAddress {
	return machine.ReturnAddress{Resource: machine.Resource{Class: machine.ResourceGeneralRegister, Number: 1}}
}
func (Policy) NativeByteOrder() op.ByteOrder         { return op.ByteOrderLittleEndian }
func (*ByteAssembler) NativeByteOrder() op.ByteOrder { return op.ByteOrderLittleEndian }
func (p Policy) Integer128Result() ([2]backend.Register, bool) {
	return [2]backend.Register{4, 5}, p.flavor == abi.C
}

// ManagedFrameLimit leaves 160 bytes of Go's 800-byte NOSPLIT reserve for
// 40-byte invocation gateway and up to 120 bytes of auto-NOSPLIT leaf frame
// (obj/loong64 marks leaves only when their full frame is < StackSmall=128).
// morestack switches to g0 without allocating a user-stack frame;
// generated outgoing argument homes are already included in the frame size.
// Keep the registration bound in goregister in sync.
func (Policy) ManagedFrameLimit() int        { return 640 }
func (Policy) CanShiftBy(w uint8) bool       { return w == 8 || w == 16 || w == 32 || w == 64 }
func (Policy) CanMulHigh(w uint8) bool       { return w == 8 || w == 16 || w == 32 || w == 64 }
func (Policy) CanMulHighSigned(w uint8) bool { return validWidth(w) }
func (Policy) CanWordCarry(w uint8) bool     { return w == 64 }
func (Policy) CanCountZeros(_ op.Opcode, w uint8) bool {
	return w == 8 || w == 16 || w == 32 || w == 64
}
func (Policy) CanAndNot(w uint8) bool { return w == 64 }
func (Policy) BranchFits(i machine.Instruction, from, to, _ int) bool {
	d := int64(to) - int64(from)
	if d%4 != 0 {
		return false
	}
	if i.Op == machine.OpJumpIf || i.Op == machine.OpBranchZero {
		if !i.Long {
			n := 18
			if i.Op == machine.OpBranchZero {
				n = 23
			}
			return d >= -(1<<(n-1)) && d < 1<<(n-1)
		}
		d -= 4
		if i.Far {
			return d >= -(1<<31) && d < (1<<31)-2048
		}
	}
	if i.Long && i.Op == machine.OpJump {
		return d >= -(1<<31) && d < (1<<31)-2048
	}
	return d >= -(1<<27) && d < 1<<27
}

// SpecialWrites distinguishes floating-point exception/status changes from
// integer flags (LA64 has no integer condition-code register).
func (Policy) SpecialWrites(i machine.Instruction) []machine.Resource {
	switch i.Op {
	case machine.OpFloatUnary, machine.OpFloatBinary, machine.OpFloatFMA, machine.OpCallC, machine.OpCallGo:
		return []machine.Resource{{Class: machine.ResourceFloatingStatus}}
	case machine.OpIntToFloat, machine.OpFloatToInt:
		// ffint and ftintrz raise inexact and invalid.
		return []machine.Resource{{Class: machine.ResourceFloatingStatus}}
	case machine.OpFloatCompare:
		// Quiet fcmp still raises invalid for a signaling NaN.
		return []machine.Resource{{Class: machine.ResourceFloatingStatus}}
	}
	return nil
}
func (Policy) ResourceName(r machine.Resource) string {
	if r.Class == machine.ResourceFloatingStatus {
		return "FCSR0"
	}
	if r.Class == machine.ResourceGeneralRegister {
		return (Policy{}).RegisterName(backend.Register(r.Number))
	}
	return ""
}

var physicalRegisters = backend.NewNamedRegisterSet(&registerNames, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31)

var policyRegisters0 = backend.NewNamedRegisterSet(&registerNames, 4, 5, 6, 7, 8, 9, 10, 11, 13, 14, 15, 16, 17, 18)
