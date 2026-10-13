package compile

import "github.com/moriyoshi/nyanjit/machine"

import target386 "github.com/moriyoshi/nyanjit/target/i386"

type i386Target struct{ target386.Policy }
type i386ByteAdapter struct{ *target386.ByteAssembler }
type i386TextAdapter struct{ *target386.TextAssembler }

func (e *i386ByteAdapter) artifact() emissionArtifact {
	return emissionArtifact{code: e.Bytes(), relocations: e.Relocations()}
}
func (e *i386TextAdapter) artifact() emissionArtifact {
	return emissionArtifact{assembly: e.Text()}
}
func newI386MachineTarget(options CompileOptions) machineTarget {
	return &i386Target{Policy: target386.NewPolicy(options.ABI, options.Stack)}
}
func newI386MachineEmitter(options CompileOptions) machineEmitter {
	emitter := &physicalEmitter{
		size:       func(ins physicalInsn) int { return target386.PhysicalSize(ins, options.Stack) },
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
	emitter.frameBytes = func(slots int, calls bool) int {
		return target386.FrameBytesForCallArea(slots, calls, emitter.callAreaBytes)
	}
	return emitter
}
func newI386MachineAssembler(options CompileOptions, kind emissionKind) machineAssembler {
	if kind == emitText {
		return &i386TextAdapter{TextAssembler: target386.NewTextAssembler(options.Stack)}
	}
	if kind == emitNative {
		return &i386ByteAdapter{ByteAssembler: target386.NewByteAssembler(options.Stack)}
	}
	return nil
}

func (*i386Target) FixedClobbers(ins machineInstruction) []physReg {
	if ins.Op == mMulHigh {
		return []physReg{0, 2}
	}
	if ins.Op == mCarryBit || ins.Op == mWordCarry {
		return []physReg{3}
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
	if ins.Op == machine.OpMulHigh {
		return []physReg{0, 2}
	}
	if ins.Op == machine.OpShiftBy {
		return []physReg{0, 1, 2, 3}
	}
	if ins.Op == machine.OpCarryBit || ins.Op == machine.OpWordCarry && ins.CarryMode != machine.CarryFlags {
		return []physReg{3}
	}
	return nil
}
