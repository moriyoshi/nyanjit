package abi

import "testing"

func TestClassifyEntryWordStorageIndependent(t *testing.T) {
	layout, err := ClassifyEntry("arm64", C, []int{1, 1}, 8, func(i int) string { return []string{"X0", "X1"}[i] }, "X0")
	if err != nil {
		t.Fatal(err)
	}
	layout.Arguments[0].Words = append(layout.Arguments[0].Words, WordLocation{Register: "appended"})
	layout.Arguments[0].Words[0].Register = "mutated"
	if layout.Arguments[1].Words[0].Register != "X1" || layout.Result.Words[0].Register != "X0" {
		t.Fatal("argument and result storage aliases")
	}
	second, err := ClassifyEntry("arm64", C, []int{1, 1}, 8, func(i int) string { return []string{"X0", "X1"}[i] }, "X0")
	if err != nil || second.Arguments[0].Words[0].Register != "X0" {
		t.Fatal("entry layouts share mutable storage")
	}
}
