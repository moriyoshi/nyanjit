package ir_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestDivisionConversionAndCompareValidation(t *testing.T) {
	param := func(bits uint8) ir.Instruction { return ir.Instruction{Op: ir.OpParam, Bits: bits} }
	cases := []struct {
		name string
		ins  []ir.Instruction
		ok   bool
	}{
		{"div", []ir.Instruction{param(32), param(32), {Op: ir.OpDiv, Bits: 32, A: 0, B: 1, Imm: uint64(ir.DivisionAllOnes)}}, true},
		{"rem signed 8", []ir.Instruction{param(8), param(8), {Op: ir.OpRemSigned, Bits: 8, A: 0, B: 1}}, true},
		{"div undefined semantics", []ir.Instruction{param(32), param(32), {Op: ir.OpDiv, Bits: 32, A: 0, B: 1, Imm: 3}}, false},
		{"div mixed widths", []ir.Instruction{param(32), param(64), {Op: ir.OpDivSigned, Bits: 32, A: 0, B: 1}}, false},
		{"from signed", []ir.Instruction{param(64), {Op: ir.OpFFromSigned, Bits: 32, A: 0}}, true},
		{"from 16 bits", []ir.Instruction{param(16), {Op: ir.OpFFromUnsigned, Bits: 64, A: 0}}, false},
		{"from with Imm", []ir.Instruction{param(64), {Op: ir.OpFFromSigned, Bits: 64, A: 0, Imm: 1}}, false},
		{"to unsigned native", []ir.Instruction{param(32), {Op: ir.OpFToUnsigned, Bits: 64, A: 0, Imm: uint64(ir.ConversionNative)}}, true},
		{"to undefined semantics", []ir.Instruction{param(64), {Op: ir.OpFToSigned, Bits: 64, A: 0, Imm: 2}}, false},
		{"to 8 bits", []ir.Instruction{param(64), {Op: ir.OpFToSigned, Bits: 8, A: 0}}, false},
		{"fcompare", []ir.Instruction{param(64), param(64), {Op: ir.OpFCompare, Bits: 64, A: 0, B: 1, Imm: uint64(ir.FloatUnorderedEqual)}}, true},
		{"fcompare undefined condition", []ir.Instruction{param(32), param(32), {Op: ir.OpFCompare, Bits: 32, A: 0, B: 1, Imm: 14}}, false},
		{"fcompare mixed widths", []ir.Instruction{param(32), param(64), {Op: ir.OpFCompare, Bits: 32, A: 0, B: 1}}, false},
		{"fcompare 16 bits", []ir.Instruction{param(16), param(16), {Op: ir.OpFCompare, Bits: 16, A: 0, B: 1}}, false},
	}
	for _, c := range cases {
		_, err := ir.Seal(&ir.Program{Instructions: c.ins, Arity: len(c.ins) - 1, Result: len(c.ins) - 1})
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok && !errors.Is(err, ir.ErrInvalidProgram) {
			t.Errorf("%s: err = %v, want ErrInvalidProgram", c.name, err)
		}
	}
}
