package abi_test

import (
	"errors"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"slices"
	"testing"
)

func Test386MixedWidthLayouts(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		entry, err := abi.ClassifyEntry("386", flavor, []int{1, 2, 1, 1, 2}, 8, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		call, err := abi.PreflightCall(abi.CallSpec{Architecture: "386", ABI: flavor, ArgumentCount: 5, ArgumentWords: []int{1, 2, 1, 1, 2}})
		if err != nil {
			t.Fatal(err)
		}
		offset := 0
		for i, n := range []int{1, 2, 1, 1, 2} {
			if len(entry.Arguments[i].Words) != n || len(call.ArgumentLocations[i].Words) != n {
				t.Fatalf("argument %d: entry=%+v call=%+v", i, entry, call)
			}
			for w := 0; w < n; w++ {
				if entry.Arguments[i].Words[w].StackOffset != 4+offset || call.ArgumentLocations[i].Words[w].StackOffset != offset {
					t.Fatalf("ABI %d argument %d word %d: entry=%+v call=%+v", flavor, i, w, entry, call)
				}
				offset += 4
			}
		}
		if flavor == abi.Go && (entry.Result.Words[0].StackOffset != 32 || call.ResultLocation.Words[0].StackOffset != 28) {
			t.Fatalf("Go result: entry=%+v call=%+v", entry.Result, call.ResultLocation)
		}
	}
}
func Test386ArgumentWordValidation(t *testing.T) {
	for _, words := range [][]int{{0}, {3}, {1, 2}} {
		_, err := abi.PreflightCall(abi.CallSpec{Architecture: "386", ABI: abi.C, ArgumentCount: 1, ArgumentWords: words})
		if !errors.Is(err, abi.ErrInvalidSpec) {
			t.Fatalf("words %v: %v", words, err)
		}
	}
	for _, words := range [][]int{{0}, {3}} {
		_, err := abi.Classify386CEntry(words)
		if !errors.Is(err, abi.ErrInvalidSpec) {
			t.Fatalf("entry words %v: %v", words, err)
		}
	}
}

func Test386NilArgumentWordsUseNativeSlots(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		for _, count := range []int{0, 1, 3, 8} {
			effect, err := abi.PreflightCall(abi.CallSpec{Architecture: "386", ABI: flavor, ArgumentCount: count})
			if err != nil {
				t.Fatal(err)
			}
			if len(effect.ArgumentLocations) != count {
				t.Fatalf("count%d: %+v", count, effect)
			}
			for i, location := range effect.ArgumentLocations {
				if len(location.Words) != 1 || location.Words[0].Storage != abi.Stack || location.Words[0].StackOffset != 4*i {
					t.Fatalf("ABI%d argument%d: %+v", flavor, i, location)
				}
			}
			if flavor == abi.Go && (effect.ResultLocation.Words[0].StackOffset != 4*count || effect.ResultLocation.Words[1].StackOffset != 4*count+4) {
				t.Fatalf("Go result count%d: %+v", count, effect.ResultLocation)
			}
		}
	}
}

func Test386CallClobbersAllXMMRegisters(t *testing.T) {
	want := []string{"XMM0", "XMM1", "XMM2", "XMM3", "XMM4", "XMM5", "XMM6", "XMM7"}
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		effect, err := abi.PreflightCall(abi.CallSpec{Architecture: "386", ABI: flavor})
		if err != nil || !slices.Equal(effect.ClobberedFPRegisters, want) {
			t.Fatalf("ABI%d clobbers=%v error=%v", flavor, effect.ClobberedFPRegisters, err)
		}
	}
}
