package abi_test

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"slices"
	"testing"
)

func TestLoong64Contracts(t *testing.T) {
	p := loong64.NewPolicy(abi.C, abi.StackNoUse)
	for r := range p.Registers().All() {
		if r < 4 || r > 18 || r == 12 {
			t.Fatalf("reserved register%v allocatable", r)
		}
	}
	for _, f := range []abi.Flavor{abi.C, abi.Go} {
		n := 8
		if f == abi.Go {
			n = 16
		}
		e, err := abi.PreflightCall(abi.CallSpec{Architecture: "loong64", ABI: f, ArgumentCount: n})
		if err != nil {
			t.Fatal(err)
		}
		if e.ArgumentRegisters[0] != "R4" || e.ResultRegister != "R4" || len(e.ArgumentRegisters) != n || e.StackAlignment != 16 {
			t.Fatalf("effect%+v", e)
		}
		if slices.Contains(e.ClobberedRegisters, "R22") || slices.Contains(e.ClobberedRegisters, "R2") {
			t.Fatal("runtime registers clobbered")
		}
		if !slices.Contains(e.ClobberedSpecialRegisters, "FCSR0") {
			t.Fatal("missing FP status effect")
		}
		if slices.Contains(e.ClobberedFPRegisters, "F24") != (f == abi.Go) {
			t.Fatal("LP64D FP preserved set")
		}
	}
	base, err := (abi.CPUProfile{}).Resolve("loong64")
	if err != nil || base.Level != "la64v1.0" || len(base.Extensions) != 0 {
		t.Fatal(base, err)
	}
	max, err := (abi.CPUProfile{Extensions: []string{"lasx"}}).Resolve("loong64")
	if err != nil || !max.Has("lsx") || base.Supports(max) {
		t.Fatal(max, err)
	}
}
