package amd64

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func FrameBytes(slots int, hasCall bool) int {
	if hasCall {
		return ((slots*8 + 7) &^ 15) + 8
	}
	return (slots*8 + 15) &^ 15
}

func FlagEffect(ins machine.Instruction) bool {
	switch ins.Op {
	case machine.OpVector:
		return false // integer SIMD and MOVZX preserve scalar arithmetic flags
	case machine.OpCarryBit, machine.OpWordCarry, machine.OpMulHigh, machine.OpShiftBy, machine.OpBinary, machine.OpBinaryImmediate, machine.OpInc, machine.OpDec, machine.OpShift, machine.OpSelect, machine.OpSyscall, machine.OpCallC, machine.OpCallGo, machine.OpFrameEnter, machine.OpFrameLeave, machine.OpCompareImmediate, machine.OpCompareRegister:
		return true
	default:
		return false
	}
}

func MemorySize(base machine.Register) int {
	size := 3 // REX, opcode, ModRM
	if base&7 == 4 {
		size++ // SIB
	}
	if base&7 == 5 {
		size++ // disp8 = 0
	}
	return size
}

func OffsetSize(base machine.Register, disp uint64) int {
	if disp == 0 {
		return MemorySize(base)
	}
	size := 3
	if base&7 == 4 {
		size++
	}
	if int64(disp) >= -128 && int64(disp) <= 127 {
		return size + 1
	}
	return size + 4
}

func IndexedSize(base machine.Register, disp uint64) int {
	size := 4 // REX, opcode, ModRM, SIB
	if disp == 0 && base&7 != 5 {
		return size
	}
	if int64(disp) >= -128 && int64(disp) <= 127 {
		return size + 1
	}
	return size + 4
}

func ConstantSize(dst machine.Register, value uint64) int {
	if value <= 0xffffffff {
		if dst >= 8 {
			return 6
		}
		return 5
	}
	if int64(value) >= -1<<31 && int64(value) <= 1<<31-1 {
		return 7
	}
	return 10
}

// PhysicalSize measures an unmanaged layout. goHomes optionally reserves Go argument homes.
func PhysicalSize(ins machine.Instruction, stack abi.StackPractice, goHomes ...int) int {
	return PhysicalSizeWithCPU(ins, stack, abi.CPUProfile{}, goHomes...)
}

// PhysicalSizeWithCPU measures expansion for the selected ISA.
func PhysicalSizeWithCPU(ins machine.Instruction, stack abi.StackPractice, profile abi.CPUProfile, goHomes ...int) int {
	homes := 0
	if len(goHomes) != 0 {
		homes = goHomes[0]
	}
	switch ins.Op {
	case machine.OpVector, machine.OpVectorMove, machine.OpVectorSpill, machine.OpVectorReload:
		return VectorSize(ins, stack, homes)
	case machine.OpCountZeros:
		size := 0
		for _, i := range countZerosPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B, profile) {
			size += len(i.code)
		}
		return size
	case machine.OpCarryBit, machine.OpWordCarry:
		n := 0
		for _, i := range wordCarryPlan(ins) {
			n += len(i.code)
		}
		return n
	case machine.OpMulHigh:
		n := 0
		for _, i := range mulHighCPUPlan(ins.Bits, ins.A, ins.B, ins.C, profile) {
			n += len(i.code)
		}
		return n
	case machine.OpShiftBy:
		size := 0
		for _, i := range shiftByCPUPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B, ins.C, profile) {
			size += len(i.code)
		}
		return size
	case machine.OpConst:
		return ConstantSize(ins.A, ins.Imm)
	case machine.OpSymbol:
		return 10
	case machine.OpMove:
		if ins.A == ins.B {
			return 0
		}
		return 3
	case machine.OpFloatBinary, machine.OpFloatUnary, machine.OpFloatFMA, machine.OpFloatMove, machine.OpFloatBits:
		return FloatSize(ins)
	case machine.OpFloatSpill, machine.OpFloatReload:
		return FloatMemorySize(ins, stack, homes)
	case machine.OpLiteralLoad:
		return LiteralSize(ins)
	case machine.OpLiteralData:
		return len(ins.Data)
	case machine.OpBinary:
		size := 3
		if ins.Arithmetic == ir.OpMul && (ins.Bits == 0 || ins.Bits == 8 || ins.Bits == 64) {
			size = 4
		} else if ins.Arithmetic == ir.OpMul && ins.Bits == 16 {
			size = 5
		} else if ins.Arithmetic == ir.OpMul {
			size = 4
		} else if ins.Bits == 16 {
			size = 4
		}
		if ins.A != ins.B {
			size += 3
		}
		return size
	case machine.OpBinaryImmediate:
		size := 7
		if int64(ins.Imm) >= -128 && int64(ins.Imm) <= 127 {
			size = 4
		}
		if ins.A != ins.B {
			size += 3
		}
		return size
	case machine.OpInc, machine.OpDec:
		if ins.A == ins.B {
			return 3
		}
		return 6
	case machine.OpNarrow:
		if ins.Imm == 32 {
			return 3
		}
		return 4
	case machine.OpSignExtend:
		if ins.Imm == 32 {
			return 3
		}
		return 4
	case machine.OpShift:
		if code := rorxPlan(ins.Arithmetic, ins.Bits, ins.A, ins.B, uint8(ins.Imm), profile); code != nil {
			return len(code)
		}
		size := 4
		if ins.Bits == 16 {
			size++
		}
		if ins.A != ins.B {
			size += 3
		}
		return size
	case machine.OpScaled:
		return IndexedSize(ins.B, ins.Disp)
	case machine.OpSelect:
		size := 7 // TEST + CMOVNE
		if ins.A != ins.D {
			size += 3
		}
		return size
	case machine.OpLoad64:
		return MemorySize(ins.B)
	case machine.OpStore64:
		return MemorySize(ins.A)
	case machine.OpLoad8:
		return MemorySize(ins.B) + 1
	case machine.OpStore8:
		return MemorySize(ins.A)
	case machine.OpLoadAddress:
		return IndexedSize(ins.B, ins.Disp)
	case machine.OpStoreAddress:
		return IndexedSize(ins.A, ins.Disp)
	case machine.OpLoadOffset:
		return OffsetSize(ins.B, ins.Disp)
	case machine.OpStoreOffset:
		return OffsetSize(ins.A, ins.Disp)
	case machine.OpSyscall:
		return 2
	case machine.OpCallC:
		return 5
	case machine.OpCallGo:
		return 3
	case machine.OpSaveLink, machine.OpRestoreLink:
		return 0
	case machine.OpSpill, machine.OpReload:
		if stack == abi.StackPlatform {
			return 8 // RSP base needs a SIB byte.
		}
		return 7
	case machine.OpFrameEnter, machine.OpFrameLeave:
		return 7
	case machine.OpReturn:
		return 1
	case machine.OpCompareImmediate:
		return 7
	case machine.OpCompareRegister:
		return 3
	case machine.OpJump:
		return 5
	case machine.OpJumpIf:
		return 6
	case machine.OpBranchZero:
		if ins.Long {
			return 12
		}
		return 2
	case machine.OpLoop:
		if ins.Long {
			return 11
		}
		return 2
	default:
		return 0
	}
}
