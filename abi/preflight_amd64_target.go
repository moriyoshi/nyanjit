package abi

import "strconv"

func preflightAMD64Call(spec CallSpec) (CallEffect, error) {
	if spec.ArgumentCount > 6 && (spec.Kind != FunctionCall || spec.ABI != Go || spec.ArgumentCount > 7) {
		return CallEffect{}, ErrBackendLimit
	}
	effect := CallEffect{
		Architecture:   "amd64",
		Kind:           spec.Kind,
		ABI:            spec.ABI,
		ResultRegister: "RAX",
		FlagsClobbered: true,
		MayReadMemory:  true,
		MayWriteMemory: true,
		Precision:      EffectConservative,
	}
	if spec.Kind == SystemCall {
		effect.NumberRegister = "RAX"
		effect.ArgumentRegisters = append([]string(nil), []string{"RDI", "RSI", "RDX", "R10", "R8", "R9"}[:spec.ArgumentCount]...)
		effect.ClobberedRegisters = []string{"RAX", "RCX", "R11"}
		effect.Precision = EffectPlatformGuaranteed
		return effect, nil
	}
	effect.RequiresCallStack = true
	for i := 0; i < 16; i++ {
		effect.ClobberedFPRegisters = append(effect.ClobberedFPRegisters, "XMM"+strconv.Itoa(i))
	}
	if spec.ABI == Go {
		effect.StackAlignment = 8
		effect.ArgumentRegisters = append([]string(nil), []string{"RAX", "RBX", "RCX", "RDI", "RSI", "R8", "R9"}[:spec.ArgumentCount]...)
		effect.ClobberedRegisters = []string{"RAX", "RBX", "RCX", "RDX", "RSI", "RDI", "R8", "R9", "R10", "R11", "R12", "R13", "R15"}
		return effect, nil
	}
	effect.StackAlignment = 16
	effect.ArgumentRegisters = append([]string(nil), []string{"RDI", "RSI", "RDX", "RCX", "R8", "R9"}[:spec.ArgumentCount]...)
	effect.ClobberedRegisters = []string{"RAX", "RCX", "RDX", "RSI", "RDI", "R8", "R9", "R10", "R11"}
	return effect, nil
}
