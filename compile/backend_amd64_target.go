package compile

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetamd64 "github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/ir"
)

type amd64ByteAdapter struct{ *targetamd64.ByteAssembler }

func (e *amd64ByteAdapter) artifact() emissionArtifact {
	return emissionArtifact{code: e.Bytes(), relocations: e.Relocations()}
}

type amd64Target struct {
	targetamd64.Policy
	fma     bool
	profile abi.ResolvedCPU
}

func (target *amd64Target) SupportsFloatFMA() bool { return target.fma }

func newAMD64MachineTarget(options compileOptions) machineTarget {
	policy := targetamd64.NewPolicy(options.ABI, options.Stack).WithManagedCalls(options.ManagedCalls)
	profile := resolvedFeatures(options)
	return &amd64Target{Policy: policy, fma: profile.Has("fma"), profile: profile}
}

func initAMD64MachineEmitter(e *physicalEmitter, options compileOptions) {
	profile := resolvedFeatures(options)
	frameBytes := amd64FrameBytes
	if (options.ABI == abi.Go || options.ManagedCalls) && options.Stack == abi.StackPlatform {
		frameBytes = func(slots int, hasCall bool) int {
			if slots == 0 && !hasCall {
				return 0
			}
			// A Go callee may spill every register argument into the
			// caller's frame before morestack. Reserve those words below
			// generated spill slots. The top word is where Go frames save
			// BP; a stack copy adjusts it as one, so no spill may use it.
			return amd64FrameBytes(slots+6+1+1, hasCall)
		}
	}
	homes := 0
	if (options.ABI == abi.Go || options.ManagedCalls) && options.Stack == abi.StackPlatform {
		homes = 7
	}
	*e = physicalEmitter{vectorHomes: homes,
		size: func(ins physicalInsn) int {
			return targetamd64.PhysicalSizeWithCPU(ins, options.Stack, profile, homes)
		},
		flagEffect: func(ins physicalInsn) bool {
			return targetamd64.FlagFactsWithCPU(ins, profile).Modified != 0
		},
		flagFacts:  func(ins physicalInsn) flagState { return targetamd64.FlagFactsWithCPU(ins, profile) },
		frameBytes: frameBytes,
		callBytes: func(calls bool) int {
			if calls {
				return 8
			}
			return 0
		},
		stack:           options.Stack,
		budget:          options.StackBytesAvailable,
		resultReg:       0,
		binarySetsFlags: true,
		entrySaves: func() []physReg {
			if options.ManagedCalls && options.ABI == abi.C {
				return []physReg{3, 5, 12, 13, 14, 15}
			}
			return nil
		}(),
	}
}

func newAMD64MachineAssembler(options compileOptions, kind emissionKind) machineAssembler {
	goHomes := 0
	if (options.ABI == abi.Go || options.ManagedCalls) && options.Stack == abi.StackPlatform {
		goHomes = 6 + 1
	}
	if kind == emitText {
		return &amd64TextAdapter{TextAssembler: targetamd64.NewTextAssembler(options.Stack, goHomes).WithCPU(resolvedFeatures(options))}
	}
	if kind == emitNative {
		return &amd64ByteAdapter{ByteAssembler: targetamd64.NewByteAssembler(options.Stack, goHomes).WithCPU(resolvedFeatures(options))}
	}
	return nil
}

func (*amd64Target) CanReuse(ins machineInstruction, inputPosition int) bool {
	switch ins.Op {
	case mVector, mMove, mInc, mDec, mNarrow, mSignExtend, mShift, mShiftBy, mMulHigh, mWordCarry, mBinary, mBinaryImmediate:
		return inputPosition == 0
	case mScaled, mLoadAddress, mLoad64, mLoad8, mLoad16, mLoad32, mLoadOffset:
		return true
	case mDivide, mMulHighSigned:
		// The expansion reads both inputs before it writes A on every path.
		return true
	case mSelect:
		return inputPosition == 2
	default:
		return false
	}
}

func (t *amd64Target) FixedClobbers(ins machineInstruction) []physReg {
	if ins.Op == mCountZeros {
		if ins.Arithmetic == ir.OpLeadingZeros && t.profile.Has("lzcnt") || ins.Arithmetic == ir.OpTrailingZeros && ins.Bits >= 32 && t.profile.Has("bmi1") {
			return nil
		}
		return []physReg{11}
	}
	if ins.Op == mMulHigh && ins.Bits == 64 {
		if t.profile.Has("bmi2") {
			return []physReg{2, 11}
		}
		return []physReg{0, 2}
	}
	if ins.Op == mMulHighSigned {
		return amd64MulHighSignedClobbers(ins.Bits)
	}
	if ins.Op == mCarryBit || ins.Op == mWordCarry {
		return []physReg{11}
	}
	if ins.Op == mDivide {
		return amd64DivideClobbers(ins.Arithmetic, ins.Bits)
	}
	if ins.Op == mIntToFloat && ins.Arithmetic == ir.OpFFromUnsigned && ins.Imm == 64 {
		return []physReg{11}
	}
	if ins.Op == mShiftBy {
		if t.profile.Has("bmi2") && (ins.Arithmetic == ir.OpShiftLeft || ins.Arithmetic == ir.OpShiftRight || ins.Arithmetic == ir.OpShiftRightArithmetic && ins.Bits == 64) {
			return []physReg{11}
		}
		if ins.Arithmetic == ir.OpRotateLeft || ins.Arithmetic == ir.OpRotateRight {
			return []physReg{1}
		}
		return []physReg{1, 11}
	}
	return nil
}

// ImplicitWrites reports fixed expansion registers in addition to the explicit
// destination. Stable variable homes must exclude these registers too.
func (t *amd64Target) ImplicitWrites(ins machine.Instruction) []physReg {
	switch ins.Op {
	case machine.OpMulHigh:
		if ins.Bits == 64 {
			return []physReg{0, 2, 11}
		}
	case machine.OpMulHighSigned:
		return amd64MulHighSignedClobbers(ins.Bits)
	case machine.OpShiftBy:
		return []physReg{1, 11}
	case machine.OpCarryBit, machine.OpWordCarry, machine.OpCountZeros:
		return []physReg{11}
	case machine.OpDivide:
		return amd64DivideClobbers(ins.Arithmetic, ins.Bits)
	case machine.OpIntToFloat:
		if ins.Arithmetic == ir.OpFFromUnsigned && ins.Imm == 64 {
			return []physReg{11}
		}
	}
	return nil
}

// amd64DivideClobbers lists the fixed registers of the OpDivide expansion:
// DIV/IDIV use RDX:RAX, and a narrow signed divisor is sign-extended in R11.
func amd64DivideClobbers(arithmetic ir.Opcode, bits uint8) []physReg {
	if (arithmetic == ir.OpDivSigned || arithmetic == ir.OpRemSigned) && bits < 32 {
		return []physReg{0, 2, 11}
	}
	return []physReg{0, 2}
}

// amd64MulHighSignedClobbers lists the fixed registers of a signed high
// product: RDX:RAX for the one-operand IMUL, else the R11 temporary.
func amd64MulHighSignedClobbers(bits uint8) []physReg {
	if bits == 64 {
		return []physReg{0, 2}
	}
	return []physReg{11}
}
