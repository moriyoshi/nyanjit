package ir

import (
	"errors"
	"fmt"
	"testing"
)

func TestMemoryAddressWidthsAreUniform(t *testing.T) {
	for _, op := range []Opcode{OpLoad8, OpLoad16, OpLoad32, OpLoad64, OpStore8, OpStore16, OpStore32, OpStore64} {
		for _, bits := range []uint8{8, 16, 32, 64} {
			t.Run(fmt.Sprintf("%s/%d", op, bits), func(t *testing.T) {
				width := uint8(64)
				switch op {
				case OpLoad8, OpStore8:
					width = 8
				case OpLoad16, OpStore16:
					width = 16
				case OpLoad32, OpStore32:
					width = 32
				}
				p := &Program{Instructions: []Instruction{{Op: OpParam, Bits: bits}, {Op: OpConst, Bits: 64, Imm: 7}, {Op: op, Bits: width, A: 0, B: 1}}, Arity: 1, Result: 1}
				if op.ProducesValue() {
					p.Instructions[2].B = 0
				}
				err := p.Validate()
				if bits < 32 {
					if !errors.Is(err, ErrInvalidProgram) {
						t.Fatalf("narrow address accepted: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestOpcodeSentinel(t *testing.T) {
	for op := Opcode(0); op < opcodeEnd; op++ {
		if op.String() == fmt.Sprintf("Opcode(%d)", op) {
			t.Fatalf("missing name %d", op)
		}
	}
	for op := uint16(opcodeEnd); op < 256; op++ {
		if Opcode(op).ProducesValue() {
			t.Fatalf("unknown opcode %d produces a value", op)
		}
	}
}
