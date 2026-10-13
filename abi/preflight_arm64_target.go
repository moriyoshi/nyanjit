package abi

import "strconv"

func preflightARM64Call(spec CallSpec) (CallEffect, error) {
	effect := CallEffect{
		Architecture:   "arm64",
		Kind:           spec.Kind,
		ABI:            spec.ABI,
		ResultRegister: "X0",
		FlagsClobbered: true,
		MayReadMemory:  true,
		MayWriteMemory: true,
		Precision:      EffectConservative,
	}
	for i := 0; i < spec.ArgumentCount; i++ {
		effect.ArgumentRegisters = append(effect.ArgumentRegisters, "X"+strconv.Itoa(i))
	}
	if spec.Kind == SystemCall {
		effect.NumberRegister = "X8"
		// The query deliberately gives an upper bound for a raw SVC boundary.
		for i := 0; i <= 17; i++ {
			effect.ClobberedRegisters = append(effect.ClobberedRegisters, "X"+strconv.Itoa(i))
		}
		return effect, nil
	}
	effect.RequiresCallStack = true
	effect.StackAlignment = 16
	for i := 0; i < 32; i++ {
		if spec.ABI == Go || i < 8 || i >= 16 {
			effect.ClobberedFPRegisters = append(effect.ClobberedFPRegisters, "V"+strconv.Itoa(i))
		}
	}
	for i := 0; i <= 17; i++ {
		effect.ClobberedRegisters = append(effect.ClobberedRegisters, "X"+strconv.Itoa(i))
	}
	if spec.ABI == Go {
		for i := 19; i <= 27; i++ {
			effect.ClobberedRegisters = append(effect.ClobberedRegisters, "X"+strconv.Itoa(i))
		}
	}
	effect.ClobberedRegisters = append(effect.ClobberedRegisters, "X30")
	return effect, nil
}
