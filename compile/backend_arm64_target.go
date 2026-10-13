package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetarm64 "github.com/moriyoshi/nyanjit/backend/target/arm64"
)

type arm64ByteAdapter struct{ *targetarm64.ByteAssembler }

func (e *arm64ByteAdapter) artifact() emissionArtifact {
	return emissionArtifact{code: e.Bytes(), relocations: e.Relocations()}
}

type arm64Target struct {
	targetarm64.Policy
	profile abi.ResolvedCPU
	stack   abi.StackPractice
	homes   int
}

func (t *arm64Target) SupportsFloatFMA() bool { return t.profile.Has("fp") }

func newARM64MachineTarget(options compileOptions) machineTarget {
	homes := 0
	if options.Stack == abi.StackPlatform && (options.ABI == abi.Go || options.ManagedCalls) {
		homes = 9
	}
	return &arm64Target{Policy: targetarm64.NewPolicy(options.ABI, options.Stack), profile: resolvedFeatures(options), stack: options.Stack, homes: homes}
}

// arm64SizeFuncs holds the sizing functions for every stack practice and Go
// home count, so emitters, built per control fragment, need no closure.
var arm64SizeFuncs = [2][2]func(physicalInsn) int{
	{
		func(ins physicalInsn) int { return targetarm64.PhysicalSize(ins, abi.StackNoUse, 0) },
		func(ins physicalInsn) int { return targetarm64.PhysicalSize(ins, abi.StackNoUse, 9) },
	},
	{
		func(ins physicalInsn) int { return targetarm64.PhysicalSize(ins, abi.StackPlatform, 0) },
		func(ins physicalInsn) int { return targetarm64.PhysicalSize(ins, abi.StackPlatform, 9) },
	},
}

func arm64SizeFunc(stack abi.StackPractice, homes int) func(physicalInsn) int {
	if stack <= abi.StackPlatform && (homes == 0 || homes == 9) {
		return arm64SizeFuncs[stack][homes/9]
	}
	return func(ins physicalInsn) int { return targetarm64.PhysicalSize(ins, stack, homes) }
}

func initARM64MachineEmitter(e *physicalEmitter, options compileOptions) {
	frameBytes := arm64FrameBytes
	if (options.ABI == abi.Go || options.ManagedCalls) && options.Stack == abi.StackPlatform {
		frameBytes = func(slots int, hasCall bool) int {
			if slots == 0 && !hasCall {
				return 0
			}
			// A Go callee may spill all register arguments into the caller's
			// frame before morestack. Keep those words below JIT spill slots.
			return arm64FrameBytes(slots+8+1, hasCall)
		}
	}
	homes := 0
	if (options.ABI == abi.Go || options.ManagedCalls) && options.Stack == abi.StackPlatform {
		homes = 9
	}
	limit := maxSpillSlots
	if options.Stack == abi.StackPlatform {
		limit -= 1 + homes
	}
	*e = physicalEmitter{vectorHomes: homes, spillLimit: limit,
		size:         arm64SizeFunc(options.Stack, homes),
		flagEffect:   func(ins physicalInsn) bool { return targetarm64.FlagFacts(ins).Modified != 0 },
		flagFacts:    targetarm64.FlagFacts,
		frameBytes:   frameBytes,
		callBytes:    func(bool) int { return 0 },
		stack:        options.Stack,
		budget:       options.StackBytesAvailable,
		resultReg:    0,
		decSetsCarry: true,
		linkInFrame:  true,
		entrySaves: func() []physReg {
			if options.ManagedCalls && options.ABI == abi.C {
				return []physReg{19, 20, 21, 22, 23, 24, 25, 26, 27}
			}
			return nil
		}(),
		entryFloatSaves: func() []physReg {
			if options.ManagedCalls && options.ABI == abi.C {
				return []physReg{8, 9, 10, 11, 12, 13, 14, 15}
			}
			return nil
		}(),
	}
}

func newARM64MachineAssembler(options compileOptions, kind emissionKind) machineAssembler {
	goHomes := 0
	if (options.ABI == abi.Go || options.ManagedCalls) && options.Stack == abi.StackPlatform {
		goHomes = 8 + 1
	}
	if kind == emitText {
		return &arm64TextAdapter{TextAssembler: targetarm64.NewTextAssembler(options.Stack, goHomes)}
	}
	if kind == emitNative {
		return &arm64ByteAdapter{ByteAssembler: targetarm64.NewByteAssembler(options.Stack, goHomes)}
	}
	return nil
}

func (*arm64Target) CanReuse(ins machineInstruction, _ int) bool { return ins.Op != mConst }

func (t *arm64Target) ImplicitWrites(ins machine.Instruction) []backend.Register {
	if ins.Op == machine.OpFloatSpill || ins.Op == machine.OpFloatReload {
		offset := ins.Slot * 8
		if t.stack == abi.StackPlatform {
			offset += (1 + t.homes) * 8
		}
		if offset > 32760 {
			return []backend.Register{16}
		}
		return nil
	}
	return targetarm64.ImplicitWrites(ins)
}
