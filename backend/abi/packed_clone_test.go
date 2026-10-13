package abi

import "testing"

func TestSmallPackedReportClone(t *testing.T) {
	source := EmissionReport{
		CPU:               CPUProfile{Extensions: []string{"fp"}},
		ArgumentRegisters: []string{"x0"}, ClobberedRegisters: []string{"x1"},
		ClobberedFPRegisters: []string{},
		ArgumentLocations:    []ValueLocation{{Words: []WordLocation{{Register: "x0"}}}, {Words: []WordLocation{}}},
		ResultLocation:       ValueLocation{Words: []WordLocation{{Register: "x1"}}},
	}
	clone := source.Clone()
	clone.CPU.Extensions[0] = "changed"
	clone.ArgumentRegisters = append(clone.ArgumentRegisters, "append")
	clone.ArgumentLocations[0].Words = append(clone.ArgumentLocations[0].Words, WordLocation{Register: "append"})
	clone.ArgumentLocations[1].Words = append(clone.ArgumentLocations[1].Words, WordLocation{Register: "empty append"})
	if source.CPU.Extensions[0] != "fp" || clone.ClobberedRegisters[0] != "x1" || source.ArgumentLocations[0].Words[0].Register != "x0" || clone.ResultLocation.Words[0].Register != "x1" {
		t.Fatal("small packed clone aliases source or sibling storage")
	}
	if clone.ClobberedFPRegisters == nil || clone.ClobberedSpecialRegisters != nil {
		t.Fatal("small packed clone changed nil/empty shape")
	}
}

func TestPackedCloneFieldsAreIndependent(t *testing.T) {
	source := EmissionReport{
		CPU:               CPUProfile{Extensions: []string{"fp"}},
		ArgumentRegisters: []string{"x0"}, ClobberedRegisters: []string{"x1"},
		ClobberedFPRegisters: []string{}, ClobberedSpecialRegisters: nil,
		ArgumentLocations: []ValueLocation{{Words: []WordLocation{{Register: "x0"}}}, {Words: []WordLocation{}}, {Words: nil}},
		ResultLocation:    ValueLocation{Words: []WordLocation{{Register: "x1"}}},
	}
	clone := source.Clone()
	clone.ArgumentRegisters = append(clone.ArgumentRegisters, "append")
	clone.CPU.Extensions[0] = "changed"
	clone.ArgumentLocations[0].Words = append(clone.ArgumentLocations[0].Words, WordLocation{Register: "append"})
	clone.ArgumentLocations[1].Words = append(clone.ArgumentLocations[1].Words, WordLocation{Register: "empty append"})
	if clone.ClobberedRegisters[0] != "x1" || clone.ResultLocation.Words[0].Register != "x1" {
		t.Fatal("append overwrote a packed sibling")
	}
	if source.CPU.Extensions[0] != "fp" || source.ArgumentLocations[0].Words[0].Register != "x0" {
		t.Fatal("clone mutated its source")
	}
	if clone.ClobberedFPRegisters == nil || clone.ClobberedSpecialRegisters != nil || clone.ArgumentLocations[2].Words != nil {
		t.Fatal("empty slice shape changed")
	}
	effect := CallEffect{ArgumentRegisters: source.ArgumentRegisters, ArgumentLocations: source.ArgumentLocations, ResultLocation: source.ResultLocation, ClobberedRegisters: source.ClobberedRegisters}
	copied := effect.Clone()
	copied.ArgumentLocations[0].Words = append(copied.ArgumentLocations[0].Words, WordLocation{Register: "append"})
	copied.ArgumentRegisters = append(copied.ArgumentRegisters, "append")
	if copied.ResultLocation.Words[0].Register != "x1" || copied.ClobberedRegisters[0] != "x1" {
		t.Fatal("effect append overwrote a sibling")
	}
}
