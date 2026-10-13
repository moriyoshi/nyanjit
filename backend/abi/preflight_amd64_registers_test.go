package abi_test

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"slices"
	"testing"
)

func TestAMD64GoCallClobbersRuntimeScratch(t *testing.T) {
	effect, err := abi.PreflightCall(abi.CallSpec{Architecture: "amd64", Kind: abi.FunctionCall, ABI: abi.Go})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(effect.ClobberedRegisters, "R15") {
		t.Fatal("Go runtime may overwrite R15, including during GC and stack growth")
	}
	if slices.Contains(effect.ClobberedRegisters, "R14") {
		t.Fatal("R14 is the current goroutine register")
	}
}
