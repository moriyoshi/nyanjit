package abi_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestPreflightExplicitArchitecture(t *testing.T) {
	for _, test := range []struct {
		architecture, argument, result string
		flavor                         abi.Flavor
		limit                          int
	}{
		{"amd64", "RDI", "RAX", abi.C, 6},
		{"amd64", "RAX", "RAX", abi.Go, 7},
		{"arm64", "X0", "X0", abi.C, 8},
		{"arm64", "X0", "X0", abi.Go, 9},
		{"riscv64", "X10", "X10", abi.C, 8},
		{"riscv64", "X10", "X10", abi.Go, 9},
		{"ppc64le", "R3", "R3", abi.C, 8},
		{"ppc64le", "R3", "R3", abi.Go, 12},
	} {
		spec := abi.CallSpec{Architecture: test.architecture, ABI: test.flavor, ArgumentCount: test.limit}
		effect, err := abi.PreflightCall(spec)
		if err != nil {
			t.Fatal(err)
		}
		if effect.Architecture != test.architecture || effect.ArgumentRegisters[0] != test.argument || effect.ResultRegister != test.result {
			t.Fatalf("target ABI %s/%d: %+v", test.architecture, test.flavor, effect)
		}
		spec.ArgumentCount++
		if _, err := abi.PreflightCall(spec); !errors.Is(err, abi.ErrBackendLimit) {
			t.Fatalf("target arity %s/%d: %v", test.architecture, test.flavor, err)
		}
	}
}

func TestPreflightRejectsBigEndianPPC64(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		if _, err := abi.PreflightCall(abi.CallSpec{Architecture: "ppc64", ABI: flavor}); !errors.Is(err, abi.ErrUnsupported) {
			t.Fatalf("ppc64 flavor %d: %v; want ErrUnsupported", flavor, err)
		}
	}
	if _, err := (abi.CPUProfile{Level: "power9"}).Resolve("ppc64"); !errors.Is(err, abi.ErrInvalidSpec) {
		t.Fatalf("ppc64 CPU profile: %v; want ErrInvalidSpec", err)
	}
}
