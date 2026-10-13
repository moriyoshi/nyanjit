package ir_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestMemoryOrderValidation(t *testing.T) {
	for _, order := range []ir.ByteOrder{ir.ByteOrderNative, ir.ByteOrderLittleEndian} {
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

// Store64 writes all eight bytes, so its value must be 64 bits wide. Narrower
// values need an explicit cast, as Builder.Store64 already requires.
func TestStore64ValueWidth(t *testing.T) {
	for _, valueBits := range []uint8{8, 16, 32, 64} {
		for _, addressBits := range []uint8{32, 64} {
			p := &ir.Program{Result: 1, Instructions: []ir.Instruction{
				{Op: ir.OpConst, Bits: addressBits, Imm: 0x1000},
				{Op: ir.OpConst, Bits: valueBits, Imm: 0x12},
				{Op: ir.OpStore64, Bits: 64, A: 0, B: 1},
			}}
			err := p.Validate()
			if valueBits == 64 && err != nil || valueBits != 64 && !errors.Is(err, ir.ErrInvalidProgram) {
				t.Errorf("value %d address %d: Validate = %v", valueBits, addressBits, err)
			}
		}
	}
}
