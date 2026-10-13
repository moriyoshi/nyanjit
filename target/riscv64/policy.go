package riscv64

import (
	"strconv"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
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
func (Policy) PhysicalRegisters() []machine.Register {
	registers := make([]machine.Register, 32)
	for i := range registers {
		registers[i] = machine.Register(i)
	}
	return registers
}
func (Policy) Registers() []machine.Register {
	return []machine.Register{10, 11, 12, 13, 14, 15, 16, 17, 5, 6, 7, 28, 29}
}
func (Policy) ParamRegister(index int) machine.Register { return machine.Register(10 + index) }
func (Policy) ResultRegister() machine.Register         { return 10 }
func (Policy) ScratchRegister() machine.Register        { return 29 }
func (Policy) WorkspaceRegister() machine.Register      { return 9 }
func (Policy) RegisterName(reg machine.Register) string { return "X" + strconv.Itoa(int(reg)) }
func (Policy) CanScaled(scale, disp uint64) bool {
	return disp == 0 && scale != 0 && scale&(scale-1) == 0
}
func (Policy) CanCombineScaled() bool { return false }
func (Policy) CanMulAdd() bool        { return true }
func (Policy) SupportsFloatFMA() bool { return true }
func (Policy) CanBinaryImmediate(operation ir.Opcode, width uint8, value uint64) bool {
	if width != 8 && width != 16 && width != 32 && width != 64 {
		return false
	}
	immediate := int64(value)
	switch operation {
	case ir.OpAdd, ir.OpAnd, ir.OpOr, ir.OpXor:
		return immediate >= -2048 && immediate <= 2047
	case ir.OpSub:
		return immediate >= -2047 && immediate <= 2048
	default:
		return false
	}
}
func (Policy) CanFoldAddress(scale, _ uint64) bool {
	return scale != 0
}
func (Policy) CanFoldBaseOffset(uint64) bool          { return true }
func (Policy) CallTargetRegister() machine.Register   { return 31 }
func (Policy) LinkRegister() (machine.Register, bool) { return 1, true }
func (Policy) PreservesRegister(reg machine.Register) bool {
	// The allocator uses caller-saved registers only.
	return reg == 8 || reg == 9 || reg >= 18 && reg <= 27
}
func (Policy) CanReuse(ins machine.SelectedInstruction, position int) bool {
	switch ins.Op {
	case machine.SelectedFloatUnary, machine.SelectedMove, machine.SelectedBinaryImmediate, machine.SelectedDec, machine.SelectedNarrow,
		machine.SelectedSignExtend, machine.SelectedShift, machine.SelectedBinary:
		return position == 0
	case machine.SelectedScaled, machine.SelectedMulAdd, machine.SelectedLoad64,
		machine.SelectedLoad8, machine.SelectedLoadAddress, machine.SelectedLoadOffset:
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
