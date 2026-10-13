package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	targetppc64 "github.com/moriyoshi/nyanjit/backend/target/ppc64"
)

func ppc64Profile(options compileOptions) (abi.PPC64Profile, error) {
	return abi.NewPPC64Profile(targetArchitecture(options), options.ABI)
}

type ppc64Target struct {
	targetppc64.Policy
	emitter *targetppc64.Emitter
	managed bool
}

func (target *ppc64Target) ImplicitWrites(ins machine.Instruction) []backend.Register {
	return target.emitter.ImplicitWrites(ins)
}
func (target *ppc64Target) SpecialWrites(ins machine.Instruction) []machine.Resource {
	return target.emitter.SpecialWrites(ins)
}

func newPPC64MachineTarget(options compileOptions) machineTarget {
	profile, err := ppc64Profile(options)
	if err != nil {
		return nil
	}
	policy, err := targetppc64.NewPolicyForCPU(profile, options.Stack, ppcLevel(options))
	if err != nil {
		return nil
	}
	emitter, _ := targetppc64.NewEmitterForCPU(profile, options.Stack, options.ManagedCalls, ppcLevel(options))
	return &ppc64Target{Policy: policy, emitter: emitter, managed: options.ManagedCalls}
}

func initPPC64MachineEmitter(e *physicalEmitter, options compileOptions) bool {
	profile, err := ppc64Profile(options)
	if err != nil {
		return false
	}
	emitter, err := targetppc64.NewEmitterForCPU(profile, options.Stack, options.ManagedCalls, ppcLevel(options))
	if err != nil {
		return false
	}
	*e = physicalEmitter{literalFrame: options.ManagedCalls, size: emitter.Size, flagEffect: emitter.FlagEffect, flagFacts: emitter.FlagFacts,
		frameBytes: emitter.FrameBytes, callBytes: func(bool) int { return 0 },
		stack: options.Stack, budget: options.StackBytesAvailable, resultReg: 3, linkInFrame: true,
		longJumpIf: true, entrySavesForCalls: true,
		entrySaves:      ppc64EntrySaves(options),
		entryFloatSaves: ppc64EntryFloatSaves(options),
		entryResources:  ppc64EntryResources(options),
	}
	return true
}

// ppc64AssemblyAdapter separates target expansion from the concrete assembler
// visitor. It receives the PCs already assigned by the physical emitter.
type ppc64AssemblyAdapter struct {
	pc        int
	emitter   *targetppc64.Emitter
	assembler targetppc64.Assembler
}

func (adapter *ppc64AssemblyAdapter) SetPC(pc int)       { adapter.pc = pc }
func (adapter *ppc64AssemblyAdapter) AssembledSize() int { return adapter.assembler.AssembledSize() }
func (adapter *ppc64AssemblyAdapter) emitInstruction(ins machine.Instruction) error {
	if ins.Op == machine.OpLiteralData {
		return adapter.assembler.(interface{ Data([]byte) error }).Data(ins.Data)
	}
	sequence, err := adapter.emitter.Expand(ins, adapter.pc)
	if err != nil {
		return err
	}
	for _, native := range sequence {
		if err := adapter.assembler.Visit(native); err != nil {
			return err
		}
	}
	return nil
}
func (adapter *ppc64AssemblyAdapter) artifact() emissionArtifact {
	if assembler, ok := adapter.assembler.(*targetppc64.ByteAssembler); ok {
		return emissionArtifact{code: assembler.Bytes(), relocations: assembler.Relocations()}
	}
	return emissionArtifact{assembly: adapter.assembler.(*targetppc64.TextAssembler).Text()}
}

func newPPC64MachineAssembler(options compileOptions, kind emissionKind) machineAssembler {
	profile, err := ppc64Profile(options)
	if err != nil {
		return nil
	}
	emitter, err := targetppc64.NewEmitterForCPU(profile, options.Stack, options.ManagedCalls, ppcLevel(options))
	if err != nil {
		return nil
	}
	var assembler targetppc64.Assembler
	if kind == emitText {
		assembler = targetppc64.NewTextAssembler()
	} else if kind == emitNative {
		assembler = targetppc64.NewByteAssembler()
	} else {
		return nil
	}
	return &ppc64AssemblyAdapter{emitter: emitter, assembler: assembler}
}

func (target *ppc64Target) ManagedFrameLimit() int {
	if target.managed || target.ABIFlavor() == abi.Go {
		return 640
	}
	return 0
}

func ppc64EntrySaves(options compileOptions) []physReg {
	if !options.ManagedCalls || options.ABI != abi.C {
		return nil
	}
	result := []physReg{}
	// R30 is g in the runtime-originating C context, and Go preserves it.
	for r := physReg(14); r < 30; r++ {
		result = append(result, r)
	}
	return append(result, 31)
}
func ppc64EntryFloatSaves(options compileOptions) []physReg {
	if !options.ManagedCalls || options.ABI != abi.C {
		return nil
	}
	result := []physReg{}
	for r := physReg(14); r < 32; r++ {
		result = append(result, r)
	}
	return result
}
func ppc64EntryResources(options compileOptions) []entryResourceSave {
	if !options.ManagedCalls || options.ABI != abi.C {
		return nil
	}
	result := []entryResourceSave{{Save: machine.OpSaveCondition, Restore: machine.OpRestoreCondition, Words: 1, Align: 1}}
	for r := physReg(20); r < 32; r++ {
		result = append(result, entryResourceSave{Save: machine.OpSaveCVector, Restore: machine.OpRestoreCVector, Register: r, Words: 2, Align: 2})
	}
	return result
}

func ppcLevel(options compileOptions) abi.PPC64CPU {
	switch resolvedFeatures(options).Level() {
	case "power9":
		return abi.PPC64Power9
	case "power10":
		return abi.PPC64Power10
	}
	return abi.PPC64Power8
}
