package i386

import (
	"math"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
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
func (Policy) PhysicalRegisters() []machine.Register {
	return []machine.Register{0, 1, 2, 3, 4, 5, 6, 7}
}
func (p Policy) Registers() []machine.Register {
	if p.stack == abi.StackPlatform {
		return []machine.Register{0, 3, 1, 2, 6, 7}
	}
	return []machine.Register{0, 1, 2}
}
func (Policy) ParamRegister(int) machine.Register     { return 0 }
func (Policy) ResultRegister() machine.Register       { return 0 }
func (Policy) ScratchRegister() machine.Register      { return 5 }
func (Policy) WorkspaceRegister() machine.Register    { return 5 }
func (Policy) CallTargetRegister() machine.Register   { return 0 }
func (Policy) LinkRegister() (machine.Register, bool) { return 0, false }
func (p Policy) PreservesRegister(reg machine.Register) bool {
	return p.stack == abi.StackPlatform && (reg == 3 || reg == 5 || reg == 6 || reg == 7)
}
func (Policy) RegisterName(reg machine.Register) string {
	return [...]string{"EAX", "ECX", "EDX", "EBX", "ESP", "EBP", "ESI", "EDI"}[reg]
}
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
func (Policy) CanBinaryImmediate(operation ir.Opcode, width uint8, _ uint64) bool {
	if width != 32 {
		return false
	}
	switch operation {
	case ir.OpAdd, ir.OpSub, ir.OpAnd, ir.OpOr, ir.OpXor:
		return true
	}
	return false
}
func (Policy) CanFoldAddress(uint64, uint64) bool { return false }
func (Policy) CanFoldBaseOffset(uint64) bool      { return false }
func (Policy) CanReuse(ins machine.SelectedInstruction, position int) bool {
	switch ins.Op {
	case machine.SelectedMove, machine.SelectedBinaryImmediate, machine.SelectedInc, machine.SelectedDec, machine.SelectedNarrow,
		machine.SelectedSignExtend, machine.SelectedShift, machine.SelectedMulHigh, machine.SelectedWordCarry, machine.SelectedBinary:
		return position == 0
	case machine.SelectedScaled, machine.SelectedLoadAddress,
		machine.SelectedLoad64, machine.SelectedLoad8, machine.SelectedLoadOffset:
		return true
	case machine.SelectedSelect:
		return position == 2
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
