package abi

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
	if spec.ArgumentCount > 0 {
		effect.ArgumentRegisters = make([]string, 0, spec.ArgumentCount)
	}
	effect.ClobberedRegisters = make([]string, 0, 28)
	for i := 0; i < spec.ArgumentCount; i++ {
		effect.ArgumentRegisters = append(effect.ArgumentRegisters, registerNamesX[i])
	}
	if spec.Kind == SystemCall {
		effect.NumberRegister = "X8"
		// The query deliberately gives an upper bound for a raw SVC boundary.
		for i := 0; i <= 17; i++ {
			effect.ClobberedRegisters = append(effect.ClobberedRegisters, registerNamesX[i])
		}
		return effect, nil
	}
	effect.ClobberedFPRegisters = make([]string, 0, 32)
	effect.RequiresCallStack = true
	effect.StackAlignment = 16
	for i := 0; i < 32; i++ {
		if spec.ABI == Go || i < 8 || i >= 16 {
			effect.ClobberedFPRegisters = append(effect.ClobberedFPRegisters, registerNamesV[i])
		}
	}
	for i := 0; i <= 17; i++ {
		effect.ClobberedRegisters = append(effect.ClobberedRegisters, registerNamesX[i])
	}
	if spec.ABI == Go {
		for i := 19; i <= 27; i++ {
			effect.ClobberedRegisters = append(effect.ClobberedRegisters, registerNamesX[i])
		}
	}
	effect.ClobberedRegisters = append(effect.ClobberedRegisters, "X30")
	return effect, nil
}
