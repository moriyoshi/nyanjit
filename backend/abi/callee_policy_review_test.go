package abi_test

import (
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

func TestLoong64GoClobbersR21(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		e, err := abi.PreflightCall(abi.CallSpec{Architecture: "loong64", ABI: flavor})
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(e.ClobberedRegisters, "R21") != (flavor == abi.Go) {
			t.Fatalf("ABI%d R21 effect: %v", flavor, e.ClobberedRegisters)
		}
	}
	if slices.Contains(loong64.NewPolicy(abi.Go, abi.StackNoUse).Registers().AppendTo(nil), backend.Register(21)) {
		t.Fatal("R21 must stay unavailable to JIT allocation")
	}
}
func TestCalleePreservationDependsOnEntryABI(t *testing.T) {
	for _, f := range []abi.Flavor{abi.C, abi.Go} {
		l := loong64.NewPolicy(f, abi.StackPlatform)
		r := riscv64.NewPolicy(f, abi.StackPlatform)
		for _, n := range []backend.Register{24, 25, 26, 27, 28, 31} {
			if l.PreservesRegister(n) != (f == abi.C) {
				t.Errorf("loong64 ABI%d incorrectly preserves R%d", f, n)
			}
		}
		for _, n := range []backend.Register{8, 9, 18, 19, 20, 21, 22, 23, 24, 25, 26} {
			if r.PreservesRegister(n) != (f == abi.C) {
				t.Errorf("riscv64 ABI%d incorrectly preserves X%d", f, n)
			}
		}
		if !l.PreservesRegister(22) || !r.PreservesRegister(27) {
			t.Fatal("reserved g lost preservation")
		}
	}
}

func TestLP64CStackAlignmentEffect(t *testing.T) {
	for _, arch := range []string{"loong64", "riscv64"} {
		for _, count := range []int{0, 8} {
			effect, err := abi.PreflightCall(abi.CallSpec{Architecture: arch, Kind: abi.FunctionCall, ABI: abi.C, ArgumentCount: count})
			if err != nil {
				t.Fatal(err)
			}
			if !effect.RequiresCallStack || effect.StackAlignment != 16 {
				t.Errorf("%s %d args: invalid C stack effect %+v", arch, count, effect)
			}
		}
	}
}
