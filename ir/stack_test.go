package ir_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func stackAreaProgram(areas []ir.StackArea, address ir.Instruction) *ir.Program {
	return &ir.Program{Instructions: []ir.Instruction{address}, StackAreas: areas}
}

func TestStackAreaValidation(t *testing.T) {
	address := ir.Instruction{Op: ir.OpStackAddress, Bits: 64}
	for _, c := range []struct {
		name    string
		areas   []ir.StackArea
		address ir.Instruction
		ok      bool
	}{
		{"one byte", []ir.StackArea{{Bytes: 1, Align: 1}}, address, true},
		{"default width", []ir.StackArea{{Bytes: 8, Align: 8}}, ir.Instruction{Op: ir.OpStackAddress}, true},
		{"32-bit", []ir.StackArea{{Bytes: 4, Align: 4}}, ir.Instruction{Op: ir.OpStackAddress, Bits: 32}, true},
		{"all of 64 KiB", []ir.StackArea{{Bytes: 1 << 15, Align: 16}, {Bytes: 1 << 15, Align: 1}}, address, true},
		{"second area", []ir.StackArea{{Bytes: 3, Align: 2}, {Bytes: 5, Align: 4}}, ir.Instruction{Op: ir.OpStackAddress, Bits: 64, Imm: 1}, true},
		{"empty", []ir.StackArea{{Bytes: 0, Align: 1}}, address, false},
		{"too large", []ir.StackArea{{Bytes: 1<<16 + 1, Align: 1}}, address, false},
		{"sum too large", []ir.StackArea{{Bytes: 1 << 15, Align: 1}, {Bytes: 1<<15 + 1, Align: 1}}, address, false},
		{"alignment 3", []ir.StackArea{{Bytes: 8, Align: 3}}, address, false},
		{"alignment 32", []ir.StackArea{{Bytes: 8, Align: 32}}, address, false},
		{"undeclared", nil, address, false},
		{"index out of range", []ir.StackArea{{Bytes: 8, Align: 8}}, ir.Instruction{Op: ir.OpStackAddress, Bits: 64, Imm: 1}, false},
		{"16-bit", []ir.StackArea{{Bytes: 8, Align: 8}}, ir.Instruction{Op: ir.OpStackAddress, Bits: 16}, false},
		{"operand", []ir.StackArea{{Bytes: 8, Align: 8}}, ir.Instruction{Op: ir.OpStackAddress, Bits: 64, A: 1}, false},
	} {
		err := stackAreaProgram(c.areas, c.address).Validate()
		if c.ok && err != nil || !c.ok && !errors.Is(err, ir.ErrInvalidProgram) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestStackAreasCloneAndSeal(t *testing.T) {
	p := stackAreaProgram([]ir.StackArea{{Bytes: 24, Align: 8}}, ir.Instruction{Op: ir.OpStackAddress, Bits: 64})
	sealed, err := ir.Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	p.StackAreas[0].Bytes = 1
	if got := sealed.StackAreas(); len(got) != 1 || got[0] != (ir.StackArea{Bytes: 24, Align: 8}) {
		t.Fatalf("sealed areas: %v", got)
	}
	clone := sealed.Clone()
	clone.StackAreas[0].Bytes = 2
	if sealed.StackAreas()[0].Bytes != 24 {
		t.Fatal("Clone shares the stack area table")
	}
	var inputs []int
	if inputs = (ir.Instruction{Op: ir.OpStackAddress}).InputsInto(inputs); len(inputs) != 0 {
		t.Fatalf("StackAddress inputs: %v", inputs)
	}
}
