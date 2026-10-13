package amd64

import (
	"fmt"
	"math"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
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
func (Policy) PhysicalRegisters() []machine.Register {
	return []machine.Register{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
}
func (p Policy) Registers() []machine.Register {
	if p.abi == abi.Go || p.managedCalls {
		if p.stack == abi.StackPlatform {
			return []machine.Register{0, 3, 1, 2, 7, 6, 8, 9, 10, 11, 12, 13}
		}
		return []machine.Register{0, 3, 1, 2, 7, 6, 8, 9, 11}
	}
	if p.stack == abi.StackPlatform {
		return []machine.Register{0, 1, 2, 6, 7, 8, 9, 10, 11}
	}
	return []machine.Register{0, 1, 2, 6, 7, 8, 9, 11}
}
func (p Policy) ParamRegister(index int) machine.Register {
	if p.abi == abi.Go {
		return [...]machine.Register{0, 3, 1, 7, 6, 8}[index]
	}
	return [...]machine.Register{7, 6, 2, 1, 8, 9}[index]
}
func (Policy) ResultRegister() machine.Register    { return 0 }
func (Policy) ScratchRegister() machine.Register   { return 10 }
func (Policy) WorkspaceRegister() machine.Register { return 10 }
func (Policy) RegisterName(reg machine.Register) string {
	if machine.IsVectorRegister(reg) {
		return fmt.Sprintf("XMM%d", machine.VectorNumber(reg))
	}
	return [...]string{"RAX", "RCX", "RDX", "RBX", "RSP", "RBP", "RSI", "RDI", "R8", "R9", "R10", "R11", "R12", "R13", "R14", "R15"}[reg]
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
func (Policy) CanBinaryImmediate(operation ir.Opcode, width uint8, value uint64) bool {
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
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
		return true
	}
	return false
}
func (Policy) BinaryImmediateZeroExtends(_ ir.Opcode, width uint8, _ uint64) bool {
	return width == 32
}
func (Policy) CanCombineScaled() bool                   { return true }
func (p Policy) CanFoldAddress(scale, disp uint64) bool { return p.CanScaled(scale, disp) }
func (Policy) CanFoldBaseOffset(disp uint64) bool {
	return int64(disp) >= -1<<31 && int64(disp) <= 1<<31-1
}
func (Policy) CallTargetRegister() machine.Register   { return 11 }
func (Policy) LinkRegister() (machine.Register, bool) { return 0, false }

// BranchFits checks the displacement of each amd64 branch form.
func (Policy) BranchFits(ins machine.Instruction, sourcePC, targetPC, size int) bool {
	distance := int64(targetPC) - int64(sourcePC) - int64(size)
	if (ins.Op == machine.OpBranchZero || ins.Op == machine.OpLoop) && !ins.Long {
		return distance >= -128 && distance <= 127
	}
	return distance >= math.MinInt32 && distance <= math.MaxInt32
}
