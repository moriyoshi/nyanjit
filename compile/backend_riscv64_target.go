package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetriscv64 "github.com/moriyoshi/nyanjit/backend/target/riscv64"
	"github.com/moriyoshi/nyanjit/ir"
)

type riscv64Target struct {
	targetriscv64.Policy
	stack   abi.StackPractice
	goHomes int
	profile abi.ResolvedCPU
}
type riscv64ByteAdapter struct{ *targetriscv64.ByteAssembler }
type riscv64TextAdapter struct{ *targetriscv64.TextAssembler }

func (e *riscv64ByteAdapter) artifact() emissionArtifact {
	return emissionArtifact{code: e.Bytes(), relocations: e.Relocations()}
}
func (e *riscv64TextAdapter) artifact() emissionArtifact { return emissionArtifact{assembly: e.Text()} }
func (t *riscv64Target) ImplicitWrites(ins machine.Instruction) []backend.Register {
	if ins.Op == machine.OpLiteralLoad {
		return []backend.Register{31}
	}
	return targetriscv64.ImplicitWrites(ins, t.stack, t.goHomes)
}
func riscv64GoHomes(options compileOptions) int {
	if (options.ABI == abi.Go || options.ManagedCalls) && options.Stack == abi.StackPlatform {
		return 10 // Nine Go argument homes and one return home.
	}
	return 0
}
func newRISCV64MachineTarget(options compileOptions) machineTarget {
	return &riscv64Target{Policy: targetriscv64.NewPolicy(options.ABI, options.Stack),
		stack: options.Stack, goHomes: riscv64GoHomes(options), profile: resolvedFeatures(options)}
}
func initRISCV64MachineEmitter(e *physicalEmitter, options compileOptions) {
	profile := resolvedFeatures(options)
	goHomes := riscv64GoHomes(options)
	frameBytes := func(slots int, hasCall bool) int {
		if slots == 0 && !hasCall {
			return 0
		}
		return targetriscv64.FrameBytes(slots+goHomes, hasCall)
	}
	*e = physicalEmitter{
		size: func(ins physicalInsn) int {
			return targetriscv64.PhysicalSizeWithCPU(ins, options.Stack, profile, goHomes)
		},
		flagEffect: targetriscv64.FlagEffect,
		frameBytes: frameBytes,
		callBytes:  func(bool) int { return 0 },
		stack:      options.Stack, budget: options.StackBytesAvailable,
		resultReg: 10, linkInFrame: true, longJump: true, longJumpIf: true, farJumpIf: true,
		// All C-preserved registers must survive a transitive Go call, even
		// if this body does not allocate them. Leaf managed entries only need
		// their actual allocated saves, planned by the shared spill scheduler.
		entrySavesForCalls: true,
		entrySaves: func() []physReg {
			if options.ManagedCalls && options.ABI == abi.C {
				return []physReg{8, 9, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27}
			}
			return nil
		}(),
		entryFloatSaves: func() []physReg {
			if options.ManagedCalls && options.ABI == abi.C {
				return []physReg{8, 9, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27}
			}
			return nil
		}(),
	}
}
func newRISCV64MachineAssembler(options compileOptions, kind emissionKind) machineAssembler {
	goHomes := riscv64GoHomes(options)
	if kind == emitText {
		assembler := targetriscv64.NewTextAssembler(options.Stack, goHomes)
		assembler.WithCPU(resolvedFeatures(options))
		return &riscv64TextAdapter{TextAssembler: assembler}
	}
	if kind == emitNative {
		return &riscv64ByteAdapter{ByteAssembler: targetriscv64.NewByteAssembler(options.Stack, goHomes).WithCPU(resolvedFeatures(options))}
	}
	return nil
}

func (t *riscv64Target) SupportsFloatFMA() bool { return t.profile.Has("f") && t.profile.Has("d") }

func (t *riscv64Target) CanCountZeros(op ir.Opcode, w uint8) bool {
	return t.profile.Has("zbb") && (w == 32 || w == 64 || op == ir.OpLeadingZeros && (w == 8 || w == 16))
}

func (t *riscv64Target) CanAndNot(w uint8) bool { return t.profile.Has("zbb") && w == 64 }
