package abi

import "strconv"

func preflightRISCV64Call(spec CallSpec) (CallEffect, error) {
	effect := CallEffect{
		Architecture: "riscv64", Kind: spec.Kind, ABI: spec.ABI,
		ResultRegister: "X10", MayReadMemory: true, MayWriteMemory: true,
		Precision: EffectConservative,
	}
	for i := 0; i < spec.ArgumentCount; i++ {
		reg := 10 + i
		if spec.ABI == Go && i == 8 {
			reg = 8
		}
		effect.ArgumentRegisters = append(effect.ArgumentRegisters, "X"+strconv.Itoa(reg))
	}
	if spec.Kind == SystemCall {
		effect.NumberRegister = "X17"
		for _, reg := range []int{5, 6, 7, 10, 11, 12, 13, 14, 15, 16, 17, 28, 29, 30, 31} {
			effect.ClobberedRegisters = append(effect.ClobberedRegisters, "X"+strconv.Itoa(reg))
		}
		return effect, nil
	}
	effect.RequiresCallStack = true
	effect.StackAlignment = 16
	for _, reg := range []int{1, 5, 6, 7, 10, 11, 12, 13, 14, 15, 16, 17, 28, 29, 30, 31} {
		effect.ClobberedRegisters = append(effect.ClobberedRegisters, "X"+strconv.Itoa(reg))
	}
	if spec.ABI == Go {
		for _, reg := range []int{8, 9, 18, 19, 20, 21, 22, 23, 24, 25, 26} {
			effect.ClobberedRegisters = append(effect.ClobberedRegisters, "X"+strconv.Itoa(reg))
		}
	}
	for i := 0; i < 32; i++ {
		if spec.ABI == Go || i < 8 || i >= 10 && i <= 17 || i >= 28 {
			effect.ClobberedFPRegisters = append(effect.ClobberedFPRegisters, "F"+strconv.Itoa(i))
		}
	}
	return effect, nil
}
