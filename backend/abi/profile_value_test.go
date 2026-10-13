package abi_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestCPUProfilesArePlainDetachedValues(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "loong64", "riscv64", "ppc64le"} {
		t.Run(arch, func(t *testing.T) {
			request := abi.CPUProfile{}
			first, err := request.Resolve(arch)
			if err != nil {
				t.Fatal(err)
			}
			literal := abi.CPUProfile{Level: first.Level, Extensions: slices.Clone(first.Extensions)}
			if !reflect.DeepEqual(first, literal) {
				t.Fatal("resolved profile contains hidden state")
			}
			second, err := first.Resolve(arch) // Exercise a normalized cache request.
			if err != nil {
				t.Fatal(err)
			}
			third, err := first.Resolve(arch) // Cache hit must also detach its slice.
			if err != nil {
				t.Fatal(err)
			}
			if len(second.Extensions) > 0 {
				second.Extensions[0] = "changed"
				if !reflect.DeepEqual(first, literal) || !reflect.DeepEqual(third, literal) {
					t.Fatal("Resolve returned shared extensions")
				}
			}
			again, err := request.Resolve(arch)
			if err != nil || !reflect.DeepEqual(again, literal) {
				t.Fatalf("caller mutation changed canonical cache: %v", err)
			}
		})
	}
}

func TestPublicClonesPreserveSliceShapes(t *testing.T) {
	for _, empty := range []bool{false, true} {
		var strings []string
		var words []abi.WordLocation
		var locations []abi.ValueLocation
		var vectors []abi.VectorOperation
		if empty {
			strings = []string{}
			words = []abi.WordLocation{}
			locations = []abi.ValueLocation{}
			vectors = []abi.VectorOperation{}
		}
		report := abi.EmissionReport{CPU: abi.CPUProfile{Extensions: strings}, ArgumentRegisters: strings, ClobberedRegisters: strings, ClobberedFPRegisters: strings, ClobberedSpecialRegisters: strings, VectorOperations: vectors, ArgumentLocations: locations, ResultLocation: abi.ValueLocation{Words: words}}
		effect := abi.CallEffect{ArgumentRegisters: strings, ClobberedRegisters: strings, ClobberedFPRegisters: strings, ClobberedSpecialRegisters: strings, ArgumentLocations: locations, ResultLocation: abi.ValueLocation{Words: words}}
		for _, nested := range []bool{false, true} {
			if nested {
				report.ArgumentLocations = []abi.ValueLocation{{Words: words}}
				effect.ArgumentLocations = []abi.ValueLocation{{Words: words}}
			}
			if got := report.Clone(); !reflect.DeepEqual(got, report) {
				t.Fatalf("report shape changed (empty=%v,nested=%v): %#v", empty, nested, got)
			}
			if got := effect.Clone(); !reflect.DeepEqual(got, effect) {
				t.Fatalf("effect shape changed (empty=%v,nested=%v): %#v", empty, nested, got)
			}
		}
		profile := abi.CPUProfile{Extensions: strings}
		if got := profile.Clone(); !reflect.DeepEqual(got, profile) {
			t.Fatalf("profile shape changed (empty=%v): %#v", empty, got)
		}
	}
}

func TestResolvedCPUCacheIsImmutable(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "loong64", "riscv64", "ppc64le"} {
		value, err := (abi.CPUProfile{}).Resolved(arch)
		if err != nil {
			t.Fatal(err)
		}
		want := value.Profile()
		request := want.Clone()
		cached, err := request.Resolved(arch)
		if err != nil {
			t.Fatal(err)
		}
		names := cached.AppendExtensions(nil)
		if !slices.Equal(names, slices.Collect(cached.Extensions())) {
			t.Fatal("iterator order differs")
		}
		for _, name := range names {
			if !cached.Has(name) {
				t.Fatal("missing feature", name)
			}
		}
		for i := range names {
			names[i] = "changed"
		}
		names = append(names, "changed")
		detached := cached.Profile()
		for i := range detached.Extensions {
			detached.Extensions[i] = "changed"
		}
		for i := range request.Extensions {
			request.Extensions[i] = "changed"
		}
		again, err := want.Resolved(arch)
		if err != nil || again != value || !reflect.DeepEqual(value.Profile(), want) {
			t.Fatal("CPU cache changed", err)
		}
	}
}
