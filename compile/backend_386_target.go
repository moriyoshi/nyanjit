package compile

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"

	target386 "github.com/moriyoshi/nyanjit/backend/target/i386"
)

type i386Target struct{ target386.Policy }
type i386ByteAdapter struct{ *target386.ByteAssembler }
type i386TextAdapter struct{ *target386.TextAssembler }

func (e *i386ByteAdapter) artifact() emissionArtifact {
	return emissionArtifact{code: e.Bytes(), relocations: e.Relocations()}
}
func (e *i386TextAdapter) artifact() emissionArtifact {
	return emissionArtifact{assembly: e.Text()}
}
func newI386MachineTarget(options compileOptions) machineTarget {
	return &i386Target{Policy: target386.NewPolicy(options.ABI, options.Stack)}
}

// i386SizeFuncs avoids a sizing closure per emitter.
var i386SizeFuncs = [2]func(physicalInsn) int{
	func(ins physicalInsn) int { return target386.PhysicalSize(ins, abi.StackNoUse, 0) },
	func(ins physicalInsn) int { return target386.PhysicalSize(ins, abi.StackPlatform, 0) },
}

func i386SizeFunc(stack abi.StackPractice) func(physicalInsn) int {
	if stack <= abi.StackPlatform {
		return i386SizeFuncs[stack]
	}
	return func(ins physicalInsn) int { return target386.PhysicalSize(ins, stack, 0) }
}

func initI386MachineEmitter(e *physicalEmitter, options compileOptions) {
	*e = physicalEmitter{
		size:       i386SizeFunc(options.Stack),
		flagEffect: target386.FlagEffect,
		callBytes: func(calls bool) int {
			if calls {
				return 4
			}
			return 0
		},
		stack:           options.Stack,
		budget:          options.StackBytesAvailable,
		resultReg:       0,
		binarySetsFlags: false,
	}
	e.frameBytes = func(slots int, calls bool) int {
		return target386.FrameBytesForCallArea(slots, calls, e.callAreaBytes)
	}
}
func newI386MachineAssembler(options compileOptions, kind emissionKind) machineAssembler {
	if kind == emitText {
		return &i386TextAdapter{TextAssembler: target386.NewTextAssembler(options.Stack)}
	}
	if kind == emitNative {
		return &i386ByteAdapter{ByteAssembler: target386.NewByteAssembler(options.Stack)}
	}
	return nil
}

func (*i386Target) FixedClobbers(ins machineInstruction) []physReg {
	if ins.Op == mMulHigh || ins.Op == mMulHighSigned {
		return []physReg{0, 2}
	}
	if ins.Op == mCarryBit || ins.Op == mWordCarry {
		return []physReg{3}
	}
	// The pair forms (64 bits) take their own paths with fixed registers.
	if ins.Op == mDivide && ins.Bits <= 32 {
		return []physReg{0, 2}
	}
	if ins.Op == mIntToFloat && ins.Arithmetic == ir.OpFFromUnsigned && ins.Imm == 32 {
		return []physReg{0}
	}
	if ins.Op == mFloatToInt && ins.Bits == 32 && ins.Arithmetic == ir.OpFToUnsigned {
		return []physReg{0, 1, 2}
	}
	return nil
}
func (*i386Target) ImplicitWrites(ins machine.Instruction) []physReg {
	if ins.Op == machine.OpBinary && ins.Arithmetic == opMul && ins.Bits == 64 {
		return []physReg{0, 1, 2, 3}
	}
	if ins.Op == machine.OpSelect && ins.Bits == 64 {
		return []physReg{0, 1, 2, 3, 6}
	}
	if ins.Op == machine.OpMulHigh || ins.Op == machine.OpMulHighSigned {
		return []physReg{0, 2}
	}
	if ins.Op == machine.OpShiftBy {
		return []physReg{0, 1, 2, 3}
	}
	if ins.Op == machine.OpCarryBit || ins.Op == machine.OpWordCarry && ins.CarryMode != machine.CarryFlags {
		return []physReg{3}
	}
	if ins.Op == machine.OpDivide {
		if ins.Bits == 64 {
			return []physReg{0, 1, 2, 3, 6, 7}
		}
		return []physReg{0, 2}
	}
	if ins.Op == machine.OpIntToFloat && ins.Arithmetic == ir.OpFFromUnsigned && ins.Imm == 32 {
		return []physReg{0}
	}
	if ins.Op == machine.OpFloatToInt && (ins.Bits == 64 || ins.Arithmetic == ir.OpFToUnsigned) {
		return []physReg{0, 1, 2}
	}
	return nil
}
