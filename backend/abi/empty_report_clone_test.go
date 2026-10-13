package abi

import (
	"testing"
	"unsafe"
)

func TestEmptyReportCloneOwnsAppendCapacity(t *testing.T) {
	strings := []string{"keep"}
	locations := []ValueLocation{{Words: []WordLocation{{Register: "keep"}}}}
	words := []WordLocation{{Register: "keep"}}
	vectors := []VectorOperation{{Position: 99}}
	source := EmissionReport{
		CPU:               CPUProfile{Extensions: strings[:0]},
		ArgumentRegisters: strings[:0], ClobberedRegisters: strings[:0],
		ClobberedFPRegisters: strings[:0], ClobberedSpecialRegisters: strings[:0],
		ArgumentLocations: locations[:0], ResultLocation: ValueLocation{Words: words[:0]},
		VectorOperations: vectors[:0],
	}
	clone := source.Clone()
	for _, field := range [][]string{clone.CPU.Extensions, clone.ArgumentRegisters, clone.ClobberedRegisters, clone.ClobberedFPRegisters, clone.ClobberedSpecialRegisters} {
		if field == nil {
			t.Fatal("non-nil empty slice became nil")
		}
		if unsafe.SliceData(field) == &strings[0] {
			t.Fatal("empty clone retains caller backing array")
		}
		_ = append(field, "changed")
	}
	if unsafe.SliceData(clone.ArgumentLocations) == &locations[0] || unsafe.SliceData(clone.ResultLocation.Words) == &words[0] || unsafe.SliceData(clone.VectorOperations) == &vectors[0] {
		t.Fatal("empty clone retains caller backing array")
	}
	_ = append(clone.ArgumentLocations, ValueLocation{})
	_ = append(clone.ResultLocation.Words, WordLocation{})
	_ = append(clone.VectorOperations, VectorOperation{})
	if strings[0] != "keep" || locations[0].Words[0].Register != "keep" || words[0].Register != "keep" || vectors[0].Position != 99 {
		t.Fatal("empty clone append mutated source storage")
	}
	if clone.ArgumentLocations == nil || clone.ResultLocation.Words == nil || clone.VectorOperations == nil {
		t.Fatal("empty clone changed slice shape")
	}
}
