//go:build linux && riscv64

package abi

import "testing"

func TestRISCV64GoCallClobbersGoScratchRegisters(t *testing.T) {
	goEffect, err := PreflightCall(CallSpec{Kind: FunctionCall, ABI: Go})
	if err != nil {
		t.Fatal(err)
	}
	cEffect, err := PreflightCall(CallSpec{Kind: FunctionCall, ABI: C})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"X24", "X25"} {
		if !containsRegister(goEffect.ClobberedRegisters, name) {
			t.Errorf("Go call omitted %s from clobbers: %v", name, goEffect.ClobberedRegisters)
		}
		if containsRegister(cEffect.ClobberedRegisters, name) {
			t.Errorf("C call reported callee-saved %s as clobbered: %v", name, cEffect.ClobberedRegisters)
		}
	}
	if containsRegister(goEffect.ClobberedRegisters, "X27") {
		t.Errorf("Go call reported current goroutine register X27 as clobbered: %v", goEffect.ClobberedRegisters)
	}
}

func containsRegister(registers []string, name string) bool {
	for _, register := range registers {
		if register == name {
			return true
		}
	}
	return false
}
