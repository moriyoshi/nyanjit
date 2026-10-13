package riscv64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func FrameBytes(slots int, hasCall bool) int {
	if slots == 0 && !hasCall {
		return 0
	}
	return ((slots+1)*8 + 15) &^ 15 // Return address at 0(SP).
}

func adjustCount(frame int) int    { return (frame + 2031) / 2032 }
func FrameEnterSize(frame int) int { return adjustCount(frame)*4 + 4 }
func FrameLeaveSize(frame int) int { return 4 + adjustCount(frame)*4 }

func FlagEffect(ins machine.Instruction) bool {
	// RISC-V branches test registers directly; there are no arithmetic flags.
	return false
}

func PhysicalSize(ins machine.Instruction, stack abi.StackPractice, goHomes int) int {
	return PhysicalSizeWithCPU(ins, stack, goHomes, abi.CPUProfile{})
}

// PhysicalSizeWithCPU measures the expansion for the selected ISA.
func PhysicalSizeWithCPU(ins machine.Instruction, stack abi.StackPractice, goHomes int, profile abi.CPUProfile) int {
	switch ins.Op {
	case machine.OpCountZeros:
		return len(countZerosWords(ins.Arithmetic, ins.Bits, ins.A, ins.B)) * 4
	case machine.OpCarryBit, machine.OpWordCarry:
		return len(wordCarryWords(ins)) * 4
	case machine.OpMulHigh:
		if ins.Bits == 64 {
			return 4
		}
		return 8
	case machine.OpShiftBy:
		return len(shiftByCPUWords(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C, profile)) * 4
	case machine.OpConst:
		return len(ConstantWords(ins.A, ins.Imm)) * 4
	case machine.OpSymbol:
		return 60
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
	case machine.OpNarrow:
		return 8
	case machine.OpSignExtend:
		if ins.Imm == 32 {
			return 4
		}
		return 8
	case machine.OpShift:
		if profile.Has("zbb") && ins.Bits >= 32 && (ins.Arithmetic == ir.OpRotateLeft || ins.Arithmetic == ir.OpRotateRight) {
			return 4
		}
		switch ins.Arithmetic {
		case ir.OpRotateLeft, ir.OpRotateRight:
			if ins.Imm == 0 {
				if ins.A == ins.B {
					return 0
				}
				return 4
			}
			return 12
		case ir.OpShiftRightArithmetic:
			if ins.Bits < 64 {
				return 8
			}
		}
		return 4
	case machine.OpScaled:
		return scaledSizeWithCPU(ins.Imm, profile)
	case machine.OpMulAdd:
		return 8
	case machine.OpSelect:
		return 16
	case machine.OpLoadAddress, machine.OpStoreAddress:
		return scaledSizeWithCPU(ins.Imm, profile) + memorySize(int64(ins.Disp))
	case machine.OpLoadOffset, machine.OpStoreOffset:
		return memorySize(int64(ins.Disp))
	case machine.OpLoadIncoming:
		return memorySize(int64(ins.Imm))
	case machine.OpSpill, machine.OpReload:
		offset := int64(ins.Slot * 8)
		if stack == abi.StackPlatform {
			offset += int64((1 + goHomes) * 8)
		}
		return memorySize(offset)
	case machine.OpSaveLink, machine.OpRestoreLink:
		if stack == abi.StackPlatform {
			return 0
		}
		return memorySize(int64(ins.Slot * 8))
	case machine.OpSaveCFloat, machine.OpRestoreCFloat:
		return memorySize(int64((ins.Slot + 1 + goHomes) * 8))
	case machine.OpFrameEnter:
		return FrameEnterSize(int(ins.Imm))
	case machine.OpFrameLeave:
		return FrameLeaveSize(int(ins.Imm))
	case machine.OpCompareRegister:
		return 0
	case machine.OpCompareImmediate:
		if ins.Imm == 0 {
			return 0
		}
		return len(ConstantWords(31, ins.Imm)) * 4
	case machine.OpJump:
		if ins.Long {
			return 8
		}
		return 4
	case machine.OpJumpIf, machine.OpBranchZero:
		if ins.Far {
			return 12
		}
		if ins.Long {
			return 8
		}
		return 4
	default:
		return 4
	}
}

func scaledSize(scale uint64) int { return scaledSizeWithCPU(scale, abi.CPUProfile{}) }
func scaledSizeWithCPU(scale uint64, p abi.CPUProfile) int {
	if p.Has("zba") && (scale == 2 || scale == 4 || scale == 8) {
		return 4
	}
	if scale == 1 {
		return 4
	}
	if scale != 0 && scale&(scale-1) == 0 {
		return 8
	}
	return len(ConstantWords(30, scale))*4 + 8
}

func memorySize(offset int64) int {
	if offset >= -2048 && offset <= 2047 {
		return 4
	}
	return len(ConstantWords(30, uint64(offset)))*4 + 8
}

// ImplicitWrites reports reserved integer registers modified by expansion of
// a physical operation. X30 and X31 are outside the allocator's register pool.
func ImplicitWrites(ins machine.Instruction, stack abi.StackPractice, goHomes int) []machine.Register {
	var x30, x31 bool
	write := func(reg machine.Register) {
		switch reg {
		case 30:
			x30 = true
		case 31:
			x31 = true
		}
	}
	far := func(offset int64) bool { return offset < -2048 || offset > 2047 }
	// Fixed destinations can be outside the allocator's ordinary pool.
	switch ins.Op {
	case machine.OpConst, machine.OpSymbol, machine.OpBinary, machine.OpBinaryImmediate, machine.OpDec,
		machine.OpNarrow, machine.OpSignExtend, machine.OpScaled, machine.OpMulAdd,
		machine.OpSelect, machine.OpLoad64, machine.OpLoad8,
		machine.OpLoadAddress, machine.OpLoadOffset, machine.OpReload, machine.OpLoadIncoming:
		write(ins.A)
	case machine.OpMove:
		if ins.A != ins.B {
			write(ins.A)
		}
	case machine.OpShift:
		if ins.Arithmetic != ir.OpRotateLeft && ins.Arithmetic != ir.OpRotateRight || ins.Imm != 0 || ins.A != ins.B {
			write(ins.A)
		}
	}
	switch ins.Op {
	case machine.OpCarryBit, machine.OpWordCarry, machine.OpShiftBy:
		x30, x31 = true, true
	case machine.OpShift:
		if ins.Arithmetic == ir.OpShiftRightArithmetic && ins.Bits < 64 {
			x30 = true
		} else if (ins.Arithmetic == ir.OpRotateLeft || ins.Arithmetic == ir.OpRotateRight) && ins.Imm != 0 {
			x30, x31 = true, true
		}
	case machine.OpScaled:
		if ins.Imm != 1 {
			x30 = true
		}
	case machine.OpMulAdd:
		x30 = true
	case machine.OpLoadAddress, machine.OpStoreAddress:
		x30 = true
		if far(int64(ins.Disp)) {
			x31 = true // The address calculation already occupies X30.
		}
	case machine.OpLoadOffset:
		if far(int64(ins.Disp)) {
			writeMemoryScratch(ins.B, &x30, &x31)
		}
	case machine.OpStoreOffset:
		if far(int64(ins.Disp)) {
			writeMemoryScratch(ins.A, &x30, &x31)
		}
	case machine.OpSpill, machine.OpReload, machine.OpFloatSpill, machine.OpFloatReload:
		offset := int64(ins.Slot * 8)
		if stack == abi.StackPlatform {
			offset += int64((1 + goHomes) * 8)
		}
		if far(offset) {
			x30 = true
		}
	case machine.OpSaveLink, machine.OpRestoreLink:
		if stack != abi.StackPlatform && far(int64(ins.Slot*8)) {
			x30 = true
		}
	case machine.OpSaveCFloat, machine.OpRestoreCFloat:
		if far(int64((ins.Slot + 1 + goHomes) * 8)) {
			x30 = true
		}
	case machine.OpLoadIncoming:
		if far(int64(ins.Imm)) {
			x30 = true
		}
	case machine.OpCompareImmediate:
		if ins.Imm != 0 {
			x31 = true
		}
	case machine.OpJump:
		if ins.Long {
			x30 = true
		}
	case machine.OpJumpIf:
		if ins.Far {
			x30 = true
		}
	}
	var registers []machine.Register
	if x30 {
		registers = append(registers, 30)
	}
	if x31 {
		registers = append(registers, 31)
	}
	return registers
}

func writeMemoryScratch(base machine.Register, x30, x31 *bool) {
	if base == 30 {
		*x31 = true
	} else {
		*x30 = true
	}
}
