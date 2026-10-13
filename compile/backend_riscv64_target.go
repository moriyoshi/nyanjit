package compile

import (
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	targetriscv64 "github.com/moriyoshi/nyanjit/target/riscv64"
)

type riscv64Target struct {
	targetriscv64.Policy
	stack   abi.StackPractice
	goHomes int
	profile abi.CPUProfile
}
type riscv64ByteAdapter struct{ *targetriscv64.ByteAssembler }
type riscv64TextAdapter struct{ *targetriscv64.TextAssembler }

func (e *riscv64ByteAdapter) artifact() emissionArtifact {
	return emissionArtifact{code: e.Bytes(), relocations: e.Relocations()}
}
func (e *riscv64TextAdapter) artifact() emissionArtifact { return emissionArtifact{assembly: e.Text()} }
func (t *riscv64Target) ImplicitWrites(ins machine.Instruction) []machine.Register {
	if ins.Op == machine.OpLiteralLoad {
		return []machine.Register{31}
	}
	return targetriscv64.ImplicitWrites(ins, t.stack, t.goHomes)
}
func riscv64GoHomes(options CompileOptions) int {
	if (options.ABI == ABIGo || options.ManagedCalls) && options.Stack == StackPlatform {
		return 10 // Nine Go argument homes and one return home.
	}
	return 0
}
func newRISCV64MachineTarget(options CompileOptions) machineTarget {
	return &riscv64Target{Policy: targetriscv64.NewPolicy(options.ABI, options.Stack),
		stack: options.Stack, goHomes: riscv64GoHomes(options), profile: resolvedCPU(options)}
}
func newRISCV64MachineEmitter(options CompileOptions) machineEmitter {
	profile := resolvedCPU(options)
	goHomes := riscv64GoHomes(options)
	frameBytes := func(slots int, hasCall bool) int {
		if slots == 0 && !hasCall {
			return 0
		}
		return targetriscv64.FrameBytes(slots+goHomes, hasCall)
	}
	return &physicalEmitter{
		size: func(ins physicalInsn) int {
			return targetriscv64.PhysicalSizeWithCPU(ins, options.Stack, goHomes, profile)
		},
		flagEffect: targetriscv64.FlagEffect,
		frameBytes: frameBytes,
		callBytes:  func(bool) int { return 0 },
		stack:      options.Stack, budget: options.StackBytesAvailable,
		resultReg: 10, linkInFrame: true, longJump: true, longJumpIf: true, farJumpIf: true,
		entrySaves: func() []physReg {
			if options.ManagedCalls && options.ABI == ABIC {
				return []physReg{8, 9, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27}
			}
			return nil
		}(),
		entryFloatSaves: func() []physReg {
			if options.ManagedCalls && options.ABI == ABIC {
				return []physReg{8, 9, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27}
			}
			return nil
		}(),
	}
}
func newRISCV64MachineAssembler(options CompileOptions, kind emissionKind) machineAssembler {
	goHomes := riscv64GoHomes(options)
	if kind == emitText {
		assembler := targetriscv64.NewTextAssembler(options.Stack, goHomes)
		assembler.WithCPUProfile(resolvedCPU(options))
		return &riscv64TextAdapter{TextAssembler: assembler}
	}
	if kind == emitNative {
		return &riscv64ByteAdapter{ByteAssembler: targetriscv64.NewByteAssembler(options.Stack, goHomes).WithCPUProfile(resolvedCPU(options))}
	}
	return nil
}

func (t *riscv64Target) SupportsFloatFMA() bool { return t.profile.Has("f") && t.profile.Has("d") }

func (t *riscv64Target) CanCountZeros(op ir.Opcode, w uint8) bool {
	return t.profile.Has("zbb") && (w == 32 || w == 64 || op == ir.OpLeadingZeros && (w == 8 || w == 16))
}

func (t *riscv64Target) CanAndNot(w uint8) bool { return t.profile.Has("zbb") && w == 64 }
