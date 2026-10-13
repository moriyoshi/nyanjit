package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetloong64 "github.com/moriyoshi/nyanjit/backend/target/loong64"
)

type loong64Target struct {
	targetloong64.Policy
	stack   abi.StackPractice
	goHomes int
}
type loong64ByteAdapter struct{ *targetloong64.ByteAssembler }
type loong64TextAdapter struct{ *targetloong64.TextAssembler }

func (e *loong64ByteAdapter) artifact() emissionArtifact {
	return emissionArtifact{code: e.Bytes(), relocations: e.Relocations()}
}
func (e *loong64TextAdapter) artifact() emissionArtifact { return emissionArtifact{assembly: e.Text()} }
func (t *loong64Target) ImplicitWrites(i machine.Instruction) []backend.Register {
	return targetloong64.ImplicitWrites(i, t.stack, t.goHomes)
}
func newLoong64MachineTarget(o compileOptions) machineTarget {
	return &loong64Target{targetloong64.NewPolicy(o.ABI, o.Stack), o.Stack, loong64GoHomes(o)}
}
func loong64GoHomes(o compileOptions) int {
	if (o.ABI == abi.Go || o.ManagedCalls) && o.Stack == abi.StackPlatform {
		return 17 // Sixteen Go register-argument homes and one result home.
	}
	return 0
}

// loong64SizeFuncs avoids a sizing closure per emitter.
var loong64SizeFuncs = [2][2]func(physicalInsn) int{
	{
		func(i physicalInsn) int { return targetloong64.PhysicalSize(i, abi.StackNoUse, 0) },
		func(i physicalInsn) int { return targetloong64.PhysicalSize(i, abi.StackNoUse, 17) },
	},
	{
		func(i physicalInsn) int { return targetloong64.PhysicalSize(i, abi.StackPlatform, 0) },
		func(i physicalInsn) int { return targetloong64.PhysicalSize(i, abi.StackPlatform, 17) },
	},
}

func loong64SizeFunc(stack abi.StackPractice, homes int) func(physicalInsn) int {
	if stack <= abi.StackPlatform && (homes == 0 || homes == 17) {
		return loong64SizeFuncs[stack][homes/17]
	}
	return func(i physicalInsn) int { return targetloong64.PhysicalSize(i, stack, homes) }
}

func initLoong64MachineEmitter(e *physicalEmitter, o compileOptions) {
	*e = physicalEmitter{size: loong64SizeFunc(o.Stack, loong64GoHomes(o)), flagEffect: targetloong64.FlagEffect,
		frameBytes: func(slots int, call bool) int {
			if slots == 0 && !call {
				return 0
			}
			return targetloong64.FrameBytes(slots+loong64GoHomes(o), call)
		}, callBytes: func(bool) int { return 0 }, stack: o.Stack, budget: o.StackBytesAvailable,
		resultReg: 4, linkInFrame: true, longJump: true, longJumpIf: true, farJumpIf: true,
		// All C-preserved registers must survive a transitive Go call, even
		// if this body does not allocate them. Leaf managed entries only need
		// their actual allocated saves, planned by the shared spill scheduler.
		entrySavesForCalls: true,
		entrySaves: func() []physReg {
			if o.ManagedCalls && o.ABI == abi.C {
				return []physReg{23, 24, 25, 26, 27, 28, 29, 30, 31}
			}
			return nil
		}(),
		entryFloatSaves: func() []physReg {
			if o.ManagedCalls && o.ABI == abi.C {
				return []physReg{24, 25, 26, 27, 28, 29, 30, 31}
			}
			return nil
		}(),
	}
}
func newLoong64MachineAssembler(o compileOptions, k emissionKind) machineAssembler {
	if k == emitText {
		return &loong64TextAdapter{targetloong64.NewTextAssembler(o.Stack, loong64GoHomes(o))}
	}
	if k == emitNative {
		return &loong64ByteAdapter{targetloong64.NewByteAssembler(o.Stack, loong64GoHomes(o))}
	}
	return nil
}
