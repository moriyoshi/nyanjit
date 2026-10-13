package arm64

import (
	"strconv"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// Policy describes register and addressing constraints for arm64 lowering.
type Policy struct {
	stack  abi.StackPractice
	flavor abi.Flavor
}

func NewPolicy(flavor abi.Flavor, stack abi.StackPractice) Policy {
	return Policy{stack: stack, flavor: flavor}
}

func (Policy) Architecture() string    { return "arm64" }
func (p Policy) ABIFlavor() abi.Flavor { return p.flavor }
func (Policy) RegisterBits() uint8     { return 64 }
func (Policy) GoCallMaxArguments() int { return 9 }
func (Policy) SupportsOrdinalSP() bool { return false }
func (Policy) PhysicalRegisters() []machine.Register {
	registers := make([]machine.Register, 31)
	for i := range registers {
		registers[i] = machine.Register(i)
	}
	return registers
}
func (p Policy) Registers() []machine.Register {
	if p.stack != abi.StackPlatform {
		return []machine.Register{0, 1, 2, 3, 4, 5, 6, 7, 9, 10, 11, 12, 13, 14, 15}
	}
	return []machine.Register{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
}
func (Policy) ParamRegister(index int) machine.Register { return machine.Register(index) }
func (Policy) ResultRegister() machine.Register         { return 0 }
func (Policy) ScratchRegister() machine.Register        { return 8 }
func (Policy) WorkspaceRegister() machine.Register      { return 8 }
func (Policy) RegisterName(reg machine.Register) string { return "X" + strconv.Itoa(int(reg)) }
func (Policy) CanScaled(scale, disp uint64) bool {
	return disp == 0 && scale != 0 && scale&(scale-1) == 0
}
func (Policy) CanMulAdd() bool        { return true }
func (Policy) SupportsFloatFMA() bool { return true }
func (Policy) CanBinaryImmediate(operation ir.Opcode, width uint8, value uint64) bool {
	// Narrow subtraction must retain the subtraction's carry convention.
	// A flipped 32-bit ADD cannot supply that fact; use a register SUB instead,
	// which also keeps carry branches usable without spill homes.
	if operation == ir.OpSub && width < 32 && int64(value) < 0 {
		return false
	}
	if operation == ir.OpAdd || operation == ir.OpSub {
		_, _, _, ok := addSubImmediate(operation, width, value)
		return ok
	}
	if operation == ir.OpAnd || operation == ir.OpOr || operation == ir.OpXor {
		_, _, ok := logicalImmediatePlan(width, value)
		return ok
	}
	return false
}
func (Policy) BinaryImmediateZeroExtends(operation ir.Opcode, width uint8, value uint64) bool {
	if width != 32 {
		return false
	}
	if operation == ir.OpAdd || operation == ir.OpSub {
		return true
	}
	_, dataWidth, ok := logicalImmediatePlan(width, value)
	return ok && (dataWidth == 32 || value>>32 == 0)
}
func (Policy) CanCombineScaled() bool { return false }

// addSubImmediate accepts the arm64 imm12 form, with an optional 12-bit shift.
// A negative literal exchanges ADD and SUB without changing the expression.
func addSubImmediate(operation ir.Opcode, width uint8, value uint64) (ir.Opcode, uint32, uint32, bool) {
	if (width != 8 && width != 16 && width != 32 && width != 64) ||
		operation != ir.OpAdd && operation != ir.OpSub {
		return 0, 0, 0, false
	}
	signed := int64(value)
	if signed < -(4095<<12) || signed > 4095<<12 {
		return 0, 0, 0, false
	}
	if signed < 0 {
		signed = -signed
		if operation == ir.OpAdd {
			operation = ir.OpSub
		} else {
			operation = ir.OpAdd
		}
	}
	if signed <= 4095 {
		return operation, uint32(signed), 0, true
	}
	if signed&4095 == 0 {
		return operation, uint32(signed >> 12), 12, true
	}
	return 0, 0, 0, false
}
func (Policy) CanFoldAddress(scale, disp uint64) bool {
	return disp == 0 && (scale == 1 || scale == 8)
}
func (Policy) CanFoldBaseOffset(disp uint64) bool {
	offset := int64(disp)
	return offset >= -256 && offset <= 255 || offset >= 0 && offset <= 4095*8 && offset%8 == 0
}
func (Policy) CallTargetRegister() machine.Register   { return 16 }
func (Policy) LinkRegister() (machine.Register, bool) { return 30, true }

// BranchFits checks the signed instruction displacement of B or B.cond.
func (Policy) BranchFits(ins machine.Instruction, sourcePC, targetPC, _ int) bool {
	distance := int64(targetPC) - int64(sourcePC)
	if ins.Long && (ins.Op == machine.OpBranchZero || ins.Op == machine.OpBranchBit) {
		// The unconditional B is the second word of the long expansion.
		distance -= 4
	}
	if distance%4 != 0 {
		return false
	}
	words := distance / 4
	limit := int64(1 << 25)
	if ins.Op == machine.OpJumpIf || ins.Op == machine.OpBranchZero && !ins.Long {
		limit = 1 << 18
	} else if ins.Op == machine.OpBranchBit && !ins.Long {
		limit = 1 << 13
	}
	return words >= -limit && words < limit
}
