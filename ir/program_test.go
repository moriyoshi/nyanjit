package ir_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestParameterizedEntryWithoutIncomingEdgeIsInvalid(t *testing.T) {
	program := &ir.Program{
		Instructions: []ir.Instruction{{Op: ir.OpLabelParam, Bits: 64}},
		Result:       -1,
		Control: &ir.Control{
			Labels:      []bool{true},
			LabelScopes: []int{0},
			Params:      []int{1},
			ParamBits:   [][]uint8{{64}},
			Steps: []ir.ControlStep{
				{IsLabel: true, Label: 0},
				{Term: ir.TermReturn, Start: 0, End: 1, Result: 0},
			},
		},
	}
	if err := program.Validate(); !errors.Is(err, ir.ErrInvalidProgram) || !strings.Contains(err.Error(), "entry label 0") {
		t.Fatalf("Validate = %v, want ErrInvalidProgram", err)
	}
}
