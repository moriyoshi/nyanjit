package amd64

import (
	"math"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Policy describes register and addressing constraints for amd64 lowering.
type Policy struct {
	abi          abi.Flavor
	stack        abi.StackPractice
	managedCalls bool
}

func NewPolicy(flavor abi.Flavor, stack abi.StackPractice) Policy {
	return Policy{abi: flavor, stack: stack}
}

// WithManagedCalls makes Go ABI argument registers available in a C entry.
// The lowering emitter saves the entry ABI's preserved registers.
func (p Policy) WithManagedCalls(enabled bool) Policy {
	p.managedCalls = enabled
	return p
}

func (Policy) Architecture() string    { return "amd64" }
func (p Policy) ABIFlavor() abi.Flavor { return p.abi }
func (Policy) RegisterBits() uint8     { return 64 }
func (Policy) GoCallMaxArguments() int { return 7 }
func (Policy) SupportsOrdinalSP() bool { return false }

// PhysicalRegisters returns an immutable value.
func (Policy) PhysicalRegisters() backend.RegisterSet {
	return policyRegisters0
}

// Registers returns an immutable value.
func (p Policy) Registers() backend.RegisterSet {
	if p.abi == abi.Go || p.managedCalls {
		if p.stack == abi.StackPlatform {
			return policyRegisters1
		}
		return policyRegisters2
	}
	if p.stack == abi.StackPlatform {
		return policyRegisters3
	}
	return policyRegisters4
}

// ParamRegister returns the ABI register for an integer parameter, or false
// for a stack argument or an index outside the ABI register bank. Compiler
// entry-arity limits are checked separately.
func (p Policy) ParamRegister(index int) (backend.Register, bool) {
	registers := [...]backend.Register{7, 6, 2, 1, 8, 9, 0, 0, 0}
	count := 6
	if p.abi == abi.Go {
		registers = [...]backend.Register{0, 3, 1, 7, 6, 8, 9, 10, 11}
		count = 9
	}
	if index < 0 || index >= count {
		return 0, false
	}
	return registers[index], true
}
func (Policy) ResultRegister() backend.Register    { return 0 }
func (Policy) ScratchRegister() backend.Register   { return 10 }
func (Policy) WorkspaceRegister() backend.Register { return 10 }
func (Policy) RegisterName(reg backend.Register) string {
	return registerNames[reg]
}
func (Policy) CanScaled(scale, disp uint64) bool {
	switch scale {
	case 1, 2, 4, 8:
		return int64(disp) >= -1<<31 && int64(disp) <= 1<<31-1
	default:
		return false
	}
}
func (Policy) CanMulAdd() bool { return false }

// SupportsFloatFMA describes the baseline; compiler CPU profiles opt in to FMA.
func (Policy) SupportsFloatFMA() bool { return false }
func (Policy) CanBinaryImmediate(operation op.Opcode, width uint8, value uint64) bool {
	if width != 8 && width != 16 && width != 32 && width != 64 {
		return false
	}
	if width == 64 && (int64(value) < math.MinInt32 || int64(value) > math.MaxInt32) {
		return false
	}
	if width < 64 && value > math.MaxUint32 && (int64(value) < math.MinInt32 || int64(value) > math.MaxInt32) {
		return false
	}
	switch operation {
	case op.OpAdd, op.OpSub, op.OpAnd, op.OpOr, op.OpXor:
		return true
	}
	return false
}
func (Policy) BinaryImmediateZeroExtends(_ op.Opcode, width uint8, _ uint64) bool {
	return width == 32
}
func (Policy) CanCombineScaled() bool                   { return true }
func (p Policy) CanFoldAddress(scale, disp uint64) bool { return p.CanScaled(scale, disp) }
func (Policy) CanFoldBaseOffset(disp uint64) bool {
	return int64(disp) >= -1<<31 && int64(disp) <= 1<<31-1
}
func (Policy) CallTargetRegister() backend.Register   { return 11 }
func (Policy) LinkRegister() (backend.Register, bool) { return 0, false }

// BranchFits checks the displacement of each amd64 branch form.
func (Policy) BranchFits(ins machine.Instruction, sourcePC, targetPC, size int) bool {
	distance := int64(targetPC) - int64(sourcePC) - int64(size)
	if (ins.Op == machine.OpBranchZero || ins.Op == machine.OpLoop) && !ins.Long {
		return distance >= -128 && distance <= 127
	}
	return distance >= math.MinInt32 && distance <= math.MaxInt32
}

var policyRegisters0 = backend.NewNamedRegisterSet(&registerNames, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15)
var policyRegisters1 = backend.NewNamedRegisterSet(&registerNames, 0, 3, 1, 2, 7, 6, 8, 9, 10, 11, 12, 13)
var policyRegisters2 = backend.NewNamedRegisterSet(&registerNames, 0, 3, 1, 2, 7, 6, 8, 9, 11)
var policyRegisters3 = backend.NewNamedRegisterSet(&registerNames, 0, 1, 2, 6, 7, 8, 9, 10, 11)
var policyRegisters4 = backend.NewNamedRegisterSet(&registerNames, 0, 1, 2, 6, 7, 8, 9, 11)
