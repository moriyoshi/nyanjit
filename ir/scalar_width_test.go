package ir_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestScalarOperandWidths(t *testing.T) {
	binary := []ir.Opcode{ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpAddScaled}
	unary := []ir.Opcode{ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight}
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, op := range append(binary, unary...) {
			p := &ir.Program{Arity: 1, Result: 2, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: width}, {Op: ir.OpConst, Bits: width, Imm: 0x7e}, {Op: op, Bits: width, A: 0, B: 1, Imm: 1}}}
			if op == ir.OpShiftLeft || op == ir.OpShiftRight || op == ir.OpShiftRightArithmetic || op == ir.OpRotateLeft || op == ir.OpRotateRight {
				p.Instructions[2].B = 0
			} else if op != ir.OpAddScaled {
				p.Instructions[2].Imm = 0
			}
			if err := p.Validate(); err != nil {
				t.Fatalf("valid width%d op%d: %v", width, op, err)
			}
			inputs := []int{0}
			if op <= ir.OpXor || op == ir.OpAddScaled {
				inputs = append(inputs, 1)
			}
			for _, other := range []uint8{8, 16, 32, 64} {
				if other == width {
					continue
				}
				for _, id := range inputs {
					q := p.Clone()
					q.Instructions[id].Bits = other
					err := q.Validate()
					if !errors.Is(err, ir.ErrInvalidProgram) || !strings.Contains(err.Error(), fmt.Sprintf("value %d has %d bits", id, other)) {
						t.Fatalf("op%d width%d operand%d width%d: %v", op, width, id, other, err)
					}
				}
			}
		}
	}
}

func TestScalarWidthExceptions(t *testing.T) {
	// A narrow condition chooses equal-width arms. Explicit casts and shift
	// counts retain their independent width contracts.
	p := &ir.Program{Arity: 2, Result: 3, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 8}, {Op: ir.OpParam, Bits: 32}, {Op: ir.OpConst, Bits: 32}, {Op: ir.OpSelect, Bits: 32, A: 0, B: 1, C: 2}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1, 2, 3} {
		q := p.Clone()
		q.Instructions[id].Bits = 16
		if err := q.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
			t.Fatalf("accepted mismatched Select operand/result%d: %v", id, err)
		}
	}
	for _, operation := range []ir.Opcode{ir.OpCastUnsigned, ir.OpSignExtend, ir.OpShiftLeftBy, ir.OpRotateRightBy, ir.OpLoad64} {
		q := &ir.Program{Arity: 2, Result: 2, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 32}, {Op: ir.OpParam, Bits: 8}, {Op: operation, Bits: 64, A: 0, B: 1, Imm: 32}}}
		q.Instructions[2].B = 0
		q.Instructions[2].Imm = 0
		if operation == ir.OpSignExtend {
			q.Instructions[2].Imm = 32
		}
		if _, by := op.ShiftByOperation(operation); by {
			q.Instructions[2].B = 1
			q.Instructions[2].Bits = 32
		}
		if err := q.Validate(); err != nil {
			t.Fatalf("valid width exception op%d: %v", operation, err)
		}
	}
}

func TestXorWidthMismatchReview(t *testing.T) {
	p := &ir.Program{Arity: 1, Result: 2, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 8}, {Op: ir.OpConst, Bits: 16, Imm: 0x7e}, {Op: ir.OpXor, Bits: 8, A: 0, B: 1}}}
	if err := p.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("review repro accepted: %v", err)
	}
	p.Instructions[1].Bits = 8
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}
