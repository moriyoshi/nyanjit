package ir_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestVariableShiftValidation(t *testing.T) {
	for _, operation := range []ir.Opcode{ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy} {
		program := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 32}, {Op: ir.OpParam, Bits: 8}, {Op: operation, Bits: 32, A: 0, B: 1}}, Arity: 2, Result: 2}
		if err := program.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, broken := range []ir.Instruction{{Op: operation, Bits: 64, A: 0, B: 1}, {Op: operation, Bits: 32, A: 0, B: 2}, {Op: operation, Bits: 32, A: -1, B: 1}} {
			copy := program.Clone()
			copy.Instructions[2] = broken
			if err := copy.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatalf("invalid variable shift %+v: %v", broken, err)
			}
		}
	}
}
