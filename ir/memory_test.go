package ir_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestMemoryOrderValidation(t *testing.T) {
	for _, order := range []ir.ByteOrder{ir.ByteOrderNative, ir.ByteOrderLittleEndian, ir.ByteOrderBigEndian} {
		program := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpConst, Imm: 0x100}, {Op: ir.OpLoad64, A: 0, ByteOrder: order}}, Result: 1}
		if err := program.Validate(); err != nil {
			t.Fatalf("valid order %d: %v", order, err)
		}
		program.Instructions[1].ByteOrder = 99
		if err := program.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
			t.Fatalf("unknown order = %v", err)
		}
	}
	program := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpConst, ByteOrder: ir.ByteOrderLittleEndian}}, Result: 0}
	if err := program.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("order on constant = %v", err)
	}
}
