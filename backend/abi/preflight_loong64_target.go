package abi

import "strconv"

// LP64D and Go ABIInternal register tables: la-abi-specs/lapcs.adoc and
// Go 1.26/1.27 internal/abi/abi_loong64.go. Go reserves R22/R29/R30.
func preflightLoong64Call(s CallSpec) (CallEffect, error) {
	e := CallEffect{Architecture: "loong64", Kind: s.Kind, ABI: s.ABI, ResultRegister: "R4", MayReadMemory: true, MayWriteMemory: true, Precision: EffectConservative}
	for i := 0; i < s.ArgumentCount; i++ {
		e.ArgumentRegisters = append(e.ArgumentRegisters, "R"+strconv.Itoa(4+i))
	}
	if s.Kind == SystemCall {
		e.NumberRegister = "R11"
	} else {
		e.RequiresCallStack = true
		e.StackAlignment = 16
	}
	e.ClobberedRegisters = append(e.ClobberedRegisters, "R1")
	for i := 4; i <= 20; i++ {
		e.ClobberedRegisters = append(e.ClobberedRegisters, "R"+strconv.Itoa(i))
	}
	if s.Kind == FunctionCall && s.ABI == Go {
		for _, i := range []int{21, 23, 24, 25, 26, 27, 28, 29, 30, 31} {
			e.ClobberedRegisters = append(e.ClobberedRegisters, "R"+strconv.Itoa(i))
		}
	}
	if s.Kind == FunctionCall {
		e.ClobberedSpecialRegisters = []string{"FCSR0"}
		for i := 0; i < 32; i++ {
			if i < 24 || s.ABI == Go {
				e.ClobberedFPRegisters = append(e.ClobberedFPRegisters, "F"+strconv.Itoa(i))
			}
		}
	}
	return e, nil
}
