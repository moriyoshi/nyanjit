package arm64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/machine"
)

// The first word of a framed function is reserved for the incoming link
// register. The Go arm64 unwinder reads it from 0(SP) while a callee runs.
// Ordinary spill slots start at 8(SP), even in a leaf with a spill, so a
// shared control-flow frame uses the same slot numbering on every path.
func FrameBytes(slots int, hasCall bool) int {
	if slots == 0 && !hasCall {
		return 0
	}
	return ((slots+1)*8 + 15) &^ 15
}

func AdjustSize(frame int) int { return ((frame + 4079) / 4080) * 4 }

func FrameEnterSize(frame int) int {
	if frame <= 240 {
		return 12 // pre-indexed LR store, saved FP, updated FP
	}
	return AdjustSize(frame) + 20 // X17, LR, FP, SP, and FP updates
}

func FrameLeaveSize(frame int) int {
	if frame <= 240 {
		return 8 // saved FP and post-indexed LR load
	}
	return AdjustSize(frame) + 8 // saved FP and LR loads
}

func FlagEffect(ins machine.Instruction) bool {
	switch ins.Op {
	case machine.OpCarryBit, machine.OpWordCarry, machine.OpShiftBy, machine.OpDec, machine.OpSelect, machine.OpSyscall, machine.OpCallC, machine.OpCallGo, machine.OpCompareImmediate, machine.OpCompareRegister:
		return true
	default:
		return false
	}
}

type ImmediatePart struct {
	Op    string
	Imm   uint16
	Shift uint8
}

func ImmediatePlan(value uint64) []ImmediatePart {
	var halves [4]uint16
	zeros, ones := 0, 0
	for i := range halves {
		halves[i] = uint16(value >> (16 * i))
		if halves[i] != 0 {
			zeros++
		}
		if halves[i] != 0xffff {
			ones++
		}
	}
	useMovN := ones < zeros
	parts := make([]ImmediatePart, 0, 4)
	for i, half := range halves {
		if useMovN && half == 0xffff || !useMovN && half == 0 {
			continue
		}
		part := ImmediatePart{Op: "movk", Imm: half, Shift: uint8(16 * i)}
		if len(parts) == 0 {
			if useMovN {
				part.Op, part.Imm = "movn", ^half
			} else {
				part.Op = "movz"
			}
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		op := "movz"
		if useMovN {
			op = "movn"
		}
		parts = append(parts, ImmediatePart{Op: op})
	}
	return parts
}

// PhysicalSize measures instructions with workspace-based vector spill homes.
func PhysicalSize(ins machine.Instruction) int {
	return PhysicalSizeWithFrame(ins, abi.StackNoUse, 0)
}

// PhysicalSizeWithFrame measures instructions for the actual stack and Go home layout.
func PhysicalSizeWithFrame(ins machine.Instruction, stack abi.StackPractice, goHomes int) int {
	switch ins.Op {
	case machine.OpVector, machine.OpVectorMove, machine.OpVectorSpill, machine.OpVectorReload:
		return VectorSize(ins, stack, goHomes)
	case machine.OpCountZeros:
		return len(countZerosPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B)) * 4
	case machine.OpCarryBit, machine.OpWordCarry:
		return wordCarrySize(ins)
	case machine.OpMulHigh:
		if ins.Bits == 64 {
			return 4
		}
		return 8
	case machine.OpShiftBy:
		return len(shiftByPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)) * 4
	case machine.OpConst:
		return len(ImmediatePlan(ins.Imm)) * 4
	case machine.OpSymbol:
		return 16
	case machine.OpMove:
		if ins.A == ins.B {
			return 0
		}
		return 4
	case machine.OpFloatBinary, machine.OpFloatUnary, machine.OpFloatFMA, machine.OpFloatMove, machine.OpFloatBits:
		return FloatSize(ins)
	case machine.OpFloatSpill, machine.OpFloatReload:
		return FloatMemorySize(ins, stack, goHomes)
	case machine.OpLiteralLoad:
		return LiteralSize(ins)
	case machine.OpLiteralData:
		return len(ins.Data)
	case machine.OpSelect:
		return 8
	case machine.OpSelectFlags:
		return 4
	case machine.OpFrameEnter:
		return FrameEnterSize(int(ins.Imm))
	case machine.OpFrameLeave:
		return FrameLeaveSize(int(ins.Imm))
	case machine.OpBranchZero, machine.OpBranchBit:
		if ins.Long {
			return 8
		}
		return 4
	default:
		return 4
	}
}

// ImplicitWrites reports reserved expansion temporaries for arithmetic plans.
func ImplicitWrites(ins machine.Instruction) []machine.Register {
	var plan []shiftByInstruction
	switch ins.Op {
	case machine.OpLiteralLoad:
		if ins.Long {
			return []machine.Register{16, 17}
		}
	case machine.OpShiftBy:
		plan = shiftByPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C)
	case machine.OpCountZeros:
		plan = countZerosPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B)
	case machine.OpCarryBit, machine.OpWordCarry:
		if ins.CarryMode != machine.CarryFlags {
			return []machine.Register{16}
		}
	}
	var x16, x17 bool
	for _, i := range plan {
		dst := i.word & 31
		x16 = x16 || dst == 16
		x17 = x17 || dst == 17
	}
	var out []machine.Register
	if x16 {
		out = append(out, 16)
	}
	if x17 {
		out = append(out, 17)
	}
	return out
}
