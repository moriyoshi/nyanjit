package abi_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
)

func TestPPC64ProfilesDescribeIndependentBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, architecture string
		flavor             abi.Flavor
		convention         abi.PPC64CABI
		pointer            abi.FunctionPointerKind
		frame              abi.PPC64FrameLayout
		order              binary.ByteOrder
		arguments          []string
	}{
		{"be-elfv1", "ppc64", abi.C, abi.PPC64ELFv1, abi.FunctionPointerDescriptor,
			abi.PPC64FrameLayout{Alignment: 16, FixedBytes: 48, ReturnAddressOffset: 16, ConditionOffset: 8, TOCOffset: 24, ParameterOffset: 48, BackChain: true}, binary.BigEndian,
			[]string{"R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10"}},
		{"be-elfv2", "ppc64", abi.C, abi.PPC64ELFv2, abi.FunctionPointerCode,
			abi.PPC64FrameLayout{Alignment: 16, FixedBytes: 32, ReturnAddressOffset: 16, ConditionOffset: 8, TOCOffset: 24, ParameterOffset: 32, BackChain: true}, binary.BigEndian,
			[]string{"R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10"}},
		{"le-elfv2", "ppc64le", abi.C, abi.PPC64CABIUnspecified, abi.FunctionPointerCode,
			abi.PPC64FrameLayout{Alignment: 16, FixedBytes: 32, ReturnAddressOffset: 16, ConditionOffset: 8, TOCOffset: 24, ParameterOffset: 32, BackChain: true}, binary.LittleEndian,
			[]string{"R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10"}},
		{"be-go", "ppc64", abi.Go, abi.PPC64CABIUnspecified, abi.FunctionPointerCode,
			abi.PPC64FrameLayout{Alignment: 8, FixedBytes: 32, ReturnAddressOffset: 0, ConditionOffset: 8, TOCOffset: 24, ParameterOffset: 32}, binary.BigEndian,
			[]string{"R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10", "R14", "R15", "R16", "R17"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile, err := abi.NewPPC64Profile(test.architecture, test.flavor, test.convention)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Frame() != test.frame || profile.FunctionPointer() != test.pointer || profile.ByteOrder() != test.order {
				t.Fatalf("profile = frame %+v, pointer %d, order %v", profile.Frame(), profile.FunctionPointer(), profile.ByteOrder())
			}
			layout, err := profile.ClassifyEntry(len(test.arguments))
			if err != nil {
				t.Fatal(err)
			}
			for i, argument := range layout.Arguments {
				if abi.SingleRegister(argument) != test.arguments[i] {
					t.Errorf("argument %d = %+v, want %s", i, argument, test.arguments[i])
				}
			}
			if _, err := profile.ClassifyEntry(len(test.arguments) + 1); !errors.Is(err, abi.ErrBackendLimit) {
				t.Fatalf("excess arity = %v", err)
			}
			effect, err := profile.PreflightCall(abi.FunctionCall, len(test.arguments))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(effect.ArgumentLocations, layout.Arguments) || effect.FunctionPointer != test.pointer || effect.StackAlignment != test.frame.Alignment {
				t.Fatalf("call and entry disagree: %+v", effect)
			}
			for _, name := range []string{"R1", "R13", "R30"} {
				if slices.Contains(effect.ClobberedRegisters, name) {
					t.Errorf("fixed register %s is clobbered", name)
				}
			}
			if !slices.Contains(effect.ClobberedSpecialRegisters, "LR") || !slices.Contains(effect.ClobberedSpecialRegisters, "CTR") {
				t.Fatalf("special call effects = %v", effect.ClobberedSpecialRegisters)
			}
			if test.flavor == abi.C && (slices.Contains(effect.ClobberedFPRegisters, "F14") || slices.Contains(effect.ClobberedFPRegisters, "V20") || slices.Contains(effect.ClobberedSpecialRegisters, "CR2")) {
				t.Fatal("C nonvolatile state is included in call clobbers")
			}
		})
	}
}

func TestPPC64PairLocationsKeepLogicalLowFirst(t *testing.T) {
	for _, test := range []struct{ architecture, low, high string }{{"ppc64", "R4", "R3"}, {"ppc64le", "R3", "R4"}} {
		profile, err := abi.NewPPC64Profile(test.architecture, abi.C, abi.PPC64ELFv2)
		if err != nil {
			t.Fatal(err)
		}
		result, err := profile.ClassifyResult(128)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Words) != 2 || result.Words[0].Register != test.low || result.Words[1].Register != test.high {
			t.Fatalf("%s result = %+v", test.architecture, result)
		}
	}
}

func TestPPC64ProfileRejectsAmbiguousAndInvalidConventions(t *testing.T) {
	for _, test := range []struct {
		arch       string
		flavor     abi.Flavor
		convention abi.PPC64CABI
	}{
		{"ppc64", abi.C, abi.PPC64CABIUnspecified}, {"ppc64le", abi.C, abi.PPC64ELFv1},
		{"ppc64", abi.Go, abi.PPC64ELFv2}, {"amd64", abi.C, abi.PPC64ELFv2},
		{"ppc64", abi.Flavor(99), abi.PPC64ELFv2}, {"ppc64", abi.C, abi.PPC64CABI(99)},
	} {
		if _, err := abi.NewPPC64Profile(test.arch, test.flavor, test.convention); err == nil {
			t.Fatalf("accepted %+v", test)
		}
	}
	var empty abi.PPC64Profile
	if _, err := empty.ClassifyEntry(0); !errors.Is(err, abi.ErrInvalidSpec) {
		t.Fatalf("zero profile = %v", err)
	}
}

func TestPPC64SyscallEffectsPreserveReturnAddress(t *testing.T) {
	profile, err := abi.NewPPC64Profile("ppc64", abi.C, abi.PPC64ELFv2)
	if err != nil {
		t.Fatal(err)
	}
	effect, err := profile.PreflightCall(abi.SystemCall, 6)
	if err != nil {
		t.Fatal(err)
	}
	if effect.NumberRegister != "R0" || effect.ResultRegister != "R3" || effect.RequiresCallStack || effect.Precision != abi.EffectPlatformGuaranteed {
		t.Fatalf("syscall = %+v", effect)
	}
	if slices.Contains(effect.ClobberedSpecialRegisters, "LR") || slices.Contains(effect.ClobberedSpecialRegisters, "CTR") || len(effect.ClobberedFPRegisters) != 0 {
		t.Fatalf("syscall clobbers preserved state: %+v", effect)
	}
	if _, err := profile.PreflightCall(abi.SystemCall, 7); !errors.Is(err, abi.ErrBackendLimit) {
		t.Fatalf("syscall excess arity = %v", err)
	}
}

func TestCallEffectCloneOwnsAllLocationsAndResources(t *testing.T) {
	profile, err := abi.NewPPC64Profile("ppc64", abi.C, abi.PPC64ELFv2)
	if err != nil {
		t.Fatal(err)
	}
	effect, err := profile.PreflightCall(abi.FunctionCall, 1)
	if err != nil {
		t.Fatal(err)
	}
	clone := effect.Clone()
	clone.ArgumentRegisters[0] = "changed"
	clone.ArgumentLocations[0].Words[0].Register = "changed"
	clone.ResultLocation.Words[0].Register = "changed"
	clone.ClobberedRegisters[0] = "changed"
	clone.ClobberedFPRegisters[0] = "changed"
	clone.ClobberedSpecialRegisters[0] = "changed"
	if effect.ArgumentRegisters[0] != "R3" || effect.ArgumentLocations[0].Words[0].Register != "R3" || effect.ResultLocation.Words[0].Register != "R3" || slices.Contains(effect.ClobberedRegisters, "changed") || slices.Contains(effect.ClobberedFPRegisters, "changed") || slices.Contains(effect.ClobberedSpecialRegisters, "changed") {
		t.Fatal("cloned effect aliases original storage")
	}
}
