package amd64

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func FrameBytes(slots int, hasCall bool) int {
	if slots < 0 || uint64(slots) > uint64(^uint(0)>>1)/16-2 {
		return 0
	}
	if hasCall {
		return ((slots*8 + 7) &^ 15) + 8
	}
	return (slots*8 + 15) &^ 15
}

func FlagEffect(ins machine.Instruction) bool {
	switch ins.Op {
	case machine.OpVector:
		return false // integer SIMD and MOVZX preserve scalar arithmetic flags
	case machine.OpBinary, machine.OpBinaryImmediate, machine.OpInc, machine.OpDec:
		return !ins.PreserveFlags // LEA
	case machine.OpCarryBit, machine.OpWordCarry, machine.OpMulHigh, machine.OpMulHighSigned, machine.OpDivide, machine.OpShiftBy, machine.OpShift, machine.OpSelect, machine.OpSyscall, machine.OpCallC, machine.OpCallGo, machine.OpFrameEnter, machine.OpFrameLeave, machine.OpCompareImmediate, machine.OpCompareRegister, machine.OpFloatCompare:
		return true
	case machine.OpIntToFloat, machine.OpFloatToInt:
		return convertWritesFlags(ins)
	default:
		return false
	}
}

func MemorySize(base backend.Register) int {
	if !validGeneralRegisters(base) {
		return 0
	}
	size := 3 // REX, opcode, ModRM
	if base&7 == 4 {
		size++ // SIB
	}
	if base&7 == 5 {
		size++ // disp8 = 0
	}
	return size
}

func OffsetSize(base backend.Register, disp uint64) int {
	if !validGeneralRegisters(base) {
		return 0
	}
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

func IndexedSize(base backend.Register, disp uint64) int {
	if !validGeneralRegisters(base) {
		return 0
	}
	size := 4 // REX, opcode, ModRM, SIB
	if disp == 0 && base&7 != 5 {
		return size
	}
	if int64(disp) >= -128 && int64(disp) <= 127 {
		return size + 1
	}
	return size + 4
}

func ConstantSize(dst backend.Register, value uint64) int {
	if !validGeneralRegisters(dst) {
		return 0
	}
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

// PhysicalSize measures instructions for the stack policy and number of Go
// argument homes. Zero goHomes selects an unmanaged layout.
func PhysicalSize(ins machine.Instruction, stack abi.StackPractice, goHomes int) int {
	if !validSizeFields(ins) {
		return 0
	}
	return PhysicalSizeWithCPU(ins, stack, abi.ResolvedCPU{}, goHomes)
}

// PhysicalSizeWithCPU measures with an immutable ISA contract.
// It panics if profile was resolved for another architecture.
func PhysicalSizeWithCPU(ins machine.Instruction, stack abi.StackPractice, profile abi.ResolvedCPU, goHomes int) int {
	if !validSizeFields(ins) || goHomes < 0 || goHomes > 4094 {
		return 0
	}

	if !validInstructionRegisters(ins) {
		return 0
	}

	if profile.Architecture() != "" && profile.Architecture() != "amd64" {
		panic("amd64: foreign CPU architecture")
	}
	homes := goHomes
	if ins.PreserveFlags {
		if f, ok := noFlagsForm(ins); ok {
			return f.size()
		}
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
	case machine.OpMulHighSigned:
		n := 0
		for _, i := range mulHighSignedPlan(ins.Bits, ins.A, ins.B, ins.C) {
			n += len(i.code)
		}
		return n
	case machine.OpDivide:
		return expansionSize(dividePlan(ins))
	case machine.OpIntToFloat, machine.OpFloatToInt:
		return expansionSize(floatConvertPlan(ins))
	case machine.OpFloatCompare:
		return expansionSize(floatComparePlan(op.FloatCondition(ins.Imm), ins.Bits, ins.A, ins.B, ins.C))
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
	case machine.OpBarrier:
		return BarrierSize(ins.Barrier)
	case machine.OpBinary:
		size := 3
		if ins.Arithmetic == op.OpMul && (ins.Bits == 0 || ins.Bits == 8 || ins.Bits == 64) {
			size = 4
		} else if ins.Arithmetic == op.OpMul && ins.Bits == 16 {
			size = 5
		} else if ins.Arithmetic == op.OpMul {
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
		if ins.Imm == 64 {
			if ins.A == ins.B {
				return 0
			}
			return 3
		}
		if ins.Imm == 32 {
			return 3
		}
		return 4
	case machine.OpSignExtend:
		if ins.Imm == 64 {
			if ins.A == ins.B {
				return 0
			}
			return 3
		}
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
	case machine.OpLoad16, machine.OpLoad32, machine.OpStore16, machine.OpStore32:
		return narrowMemorySize(ins)
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
