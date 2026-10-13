package abi_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestFloatPassingHelpers(t *testing.T) {
	p := abi.FloatArgument(0) | abi.FloatArgument(3) | abi.FloatResult32
	if !p.Argument(0) || p.Argument(1) || !p.Argument(3) || p.ResultBits() != 32 {
		t.Fatalf("%#x: marks wrong", uint64(p))
	}
	if abi.FloatArgument(-1) != 0 || abi.FloatArgument(62) != 0 || p.Argument(62) {
		t.Fatal("out-of-range argument marks")
	}
	if abi.FloatResult64.ResultBits() != 64 || (abi.FloatResult32|abi.FloatResult64).ResultBits() != 0 || abi.FloatPassing(0).ResultBits() != 0 {
		t.Fatal("result widths")
	}
}

// TestPreflightFloatRegisters checks f(a int, x float, b int, y float) with
// a float result on every convention: independent sequences, ppc64le C
// positional GPRs, and 386 stack words with a C result in ST(0).
func TestPreflightFloatRegisters(t *testing.T) {
	floats := abi.FloatArgument(1) | abi.FloatArgument(3) | abi.FloatResult64
	for _, test := range []struct {
		architecture string
		flavor       abi.Flavor
		arguments    []string
		result       string
	}{
		{"amd64", abi.C, []string{"RDI", "XMM0", "RSI", "XMM1"}, "XMM0"},
		{"amd64", abi.Go, []string{"RAX", "XMM0", "RBX", "XMM1"}, "XMM0"},
		{"arm64", abi.C, []string{"X0", "V0", "X1", "V1"}, "V0"},
		{"arm64", abi.Go, []string{"X0", "V0", "X1", "V1"}, "V0"},
		{"riscv64", abi.C, []string{"X10", "F10", "X11", "F11"}, "F10"},
		{"riscv64", abi.Go, []string{"X10", "F10", "X11", "F11"}, "F10"},
		{"ppc64le", abi.C, []string{"R3", "F1", "R5", "F2"}, "F1"},
		{"ppc64le", abi.Go, []string{"R3", "F1", "R4", "F2"}, "F1"},
		{"loong64", abi.C, []string{"R4", "F0", "R5", "F1"}, "F0"},
		{"loong64", abi.Go, []string{"R4", "F0", "R5", "F1"}, "F0"},
	} {
		effect, err := abi.PreflightCall(abi.CallSpec{Architecture: test.architecture, Kind: abi.FunctionCall, ABI: test.flavor, ArgumentCount: 4, Floats: floats})
		if err != nil {
			t.Fatalf("%s/%d: %v", test.architecture, test.flavor, err)
		}
		if !slices.Equal(effect.ArgumentRegisters, test.arguments) || effect.ResultRegister != test.result || effect.Floats != floats ||
			abi.SingleRegister(effect.ArgumentLocations[1]) != test.arguments[1] || abi.SingleRegister(effect.ResultLocation) != test.result {
			t.Errorf("%s/%d: arguments %v result %s, want %v %s", test.architecture, test.flavor, effect.ArgumentRegisters, effect.ResultRegister, test.arguments, test.result)
		}
	}
	c386, err := abi.PreflightCall(abi.CallSpec{Architecture: "386", Kind: abi.FunctionCall, ABI: abi.C, ArgumentCount: 4, ArgumentWords: []int{1, 1, 1, 2}, Floats: floats})
	if err != nil || abi.SingleRegister(c386.ResultLocation) != "ST0" || len(c386.ArgumentLocations[3].Words) != 2 || c386.ArgumentLocations[3].Words[0].Storage != abi.Stack {
		t.Errorf("386 C: %+v, %v", c386, err)
	}
	go386, err := abi.PreflightCall(abi.CallSpec{Architecture: "386", Kind: abi.FunctionCall, ABI: abi.Go, ArgumentCount: 4, ArgumentWords: []int{1, 1, 1, 2}, Floats: floats})
	if err != nil || go386.ResultLocation.Words[0].Storage != abi.Stack {
		t.Errorf("386 Go: %+v, %v", go386, err)
	}
}

func TestPreflightFloatValidation(t *testing.T) {
	nine := abi.FloatPassing(0)
	for i := range 9 {
		nine |= abi.FloatArgument(i)
	}
	for name, spec := range map[string]abi.CallSpec{
		"beyond arity": {Architecture: "arm64", ArgumentCount: 2, Floats: abi.FloatArgument(2)},
		"both results": {Architecture: "arm64", ArgumentCount: 1, Floats: abi.FloatResult32 | abi.FloatResult64},
		"nine floats":  {Architecture: "loong64", ABI: abi.Go, ArgumentCount: 9, Floats: nine},
		"system call":  {Architecture: "amd64", Kind: abi.SystemCall, ArgumentCount: 1, Floats: abi.FloatArgument(0)},
	} {
		if _, err := abi.PreflightCall(spec); !errors.Is(err, abi.ErrInvalidSpec) {
			t.Errorf("%s: %v, want ErrInvalidSpec", name, err)
		}
	}
}
