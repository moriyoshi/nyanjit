package ir_test

import (
	"errors"
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func TestWordArithmeticValidatesWidthsAndReferences(t *testing.T) {
	for _, op := range []ir.Opcode{ir.OpMulHigh, ir.OpCarryAdd, ir.OpBorrowSub, ir.OpAddWithCarry, ir.OpSubWithBorrow} {
		program := &ir.Program{Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 32}, {Op: ir.OpParam, Bits: 32}, {Op: ir.OpParam, Bits: 32}, {Op: op, Bits: 32, A: 0, B: 1, C: 2}}, Arity: 3, Result: 3}
		if err := program.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, invalid := range []ir.Instruction{{Op: op, Bits: 64, A: 0, B: 1, C: 2}, {Op: op, Bits: 32, A: 0, B: 3, C: 2}, {Op: op, Bits: 32, A: -1, B: 1, C: 2}} {
			copy := program.Clone()
			copy.Instructions[3] = invalid
			if err := copy.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatalf("invalid word arithmetic %+v: %v", invalid, err)
			}
		}
		if op == ir.OpAddWithCarry || op == ir.OpSubWithBorrow {
			copy := program.Clone()
			copy.Instructions[3].C = 3
			if err := copy.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatal("accepted cyclic carry input")
			}
		}
	}
}
