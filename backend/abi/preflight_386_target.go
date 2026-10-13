package abi

// preflightI386Call describes native words on the caller stack before CALL
// pushes the return address. C results occupy EAX:EDX.

func preflightI386Call(spec CallSpec) (CallEffect, error) {
	if spec.Kind != FunctionCall {
		return CallEffect{}, ErrUnsupported
	}
	if spec.ABI != C && spec.ABI != Go {
		return CallEffect{}, ErrUnsupportedABI
	}
	fp := []string{"XMM0", "XMM1", "XMM2", "XMM3", "XMM4", "XMM5", "XMM6", "XMM7"}
	words := spec.ArgumentWords
	if words == nil {
		words = make([]int, spec.ArgumentCount)
		for i := range words {
			words[i] = 1
		}
	}
	arguments, end, err := stack386Arguments(words, 0, MaxGoCallArgumentsFor("386"))
	if err != nil {
		return CallEffect{}, err
	}
	if spec.ABI == Go {
		effect := CallEffect{
			Architecture: "386", Kind: FunctionCall, ABI: Go,
			ArgumentRegisters:    make([]string, spec.ArgumentCount),
			ArgumentLocations:    arguments,
			ResultLocation:       stackValue(end, end+4),
			ClobberedRegisters:   []string{"EAX", "EBX", "ECX", "EDX", "EBP", "ESI", "EDI"},
			ClobberedFPRegisters: append([]string(nil), fp...),
			FlagsClobbered:       true, MayReadMemory: true, MayWriteMemory: true,
			RequiresCallStack: true, StackAlignment: 4, Precision: EffectConservative,
		}
		return effect, nil
	}
	effect := CallEffect{
		Architecture:      "386",
		Kind:              FunctionCall,
		ABI:               C,
		ArgumentRegisters: make([]string, spec.ArgumentCount),
		ArgumentLocations: arguments,
		ResultLocation: ValueLocation{Words: []WordLocation{
			{Storage: Register, Register: "EAX"},
			{Storage: Register, Register: "EDX"},
		}},
		ClobberedRegisters:   []string{"EAX", "ECX", "EDX"},
		ClobberedFPRegisters: append([]string(nil), fp...),
		FlagsClobbered:       true,
		MayReadMemory:        true,
		MayWriteMemory:       true,
		RequiresCallStack:    true,
		StackAlignment:       16,
		Precision:            EffectConservative,
	}
	return effect, nil
}
