package ir_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func shuffleProgram(typ ir.Type, data []byte) *ir.Program {
	return &ir.Program{Result: 3, Instructions: []ir.Instruction{
		{Op: ir.OpVZero, Type: typ, Bits: typ.LaneBits},
		{Op: ir.OpVZero, Type: typ, Bits: typ.LaneBits},
		{Op: ir.OpVShuffle, Type: typ, Bits: typ.LaneBits, A: 0, B: 1, Data: data},
		{Op: ir.OpConst, Bits: 32},
	}}
}

func extendProgram(from, to ir.Type, op ir.Opcode, imm uint64) *ir.Program {
	return &ir.Program{Result: 2, Instructions: []ir.Instruction{
		{Op: ir.OpVZero, Type: from, Bits: from.LaneBits},
		{Op: op, Type: to, Bits: to.LaneBits, A: 0, Imm: imm},
		{Op: ir.OpConst, Bits: 32},
	}}
}

func TestShuffleValidation(t *testing.T) {
	typ := ir.Vec(4, 32)
	for _, test := range []struct {
		name   string
		valid  bool
		change func(*ir.Program)
	}{
		{"valid", true, func(*ir.Program) {}},
		{"highest-index", true, func(p *ir.Program) { p.Instructions[2].Data = []byte{7, 7, 7, 7} }},
		{"index", false, func(p *ir.Program) { p.Instructions[2].Data = []byte{0, 1, 2, 8} }},
		{"short-data", false, func(p *ir.Program) { p.Instructions[2].Data = []byte{0, 1, 2} }},
		{"long-data", false, func(p *ir.Program) { p.Instructions[2].Data = []byte{0, 1, 2, 3, 4} }},
		{"no-data", false, func(p *ir.Program) { p.Instructions[2].Data = nil }},
		{"input-A", false, func(p *ir.Program) { p.Instructions[0].Type = ir.Vec(2, 64); p.Instructions[0].Bits = 64 }},
		{"input-B", false, func(p *ir.Program) { p.Instructions[1].Type = ir.Vec(8, 32) }},
		{"bits", false, func(p *ir.Program) { p.Instructions[2].Bits = 16 }},
		{"imm", false, func(p *ir.Program) { p.Instructions[2].Imm = 1 }},
		{"mask", false, func(p *ir.Program) { p.Instructions[2].Type = ir.Mask(4); p.Instructions[2].Bits = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := shuffleProgram(typ, []byte{3, 2, 5, 4})
			test.change(p)
			if err := p.Validate(); test.valid && err != nil || !test.valid && !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatalf("Validate=%v; want valid=%v", err, test.valid)
			}
		})
	}
	// The largest vector has 64 lanes, whose indices still fit in a byte.
	data := make([]byte, 64)
	for i := range data {
		data[i] = byte(127 - i)
	}
	if err := shuffleProgram(ir.Vec(64, 8), data).Validate(); err != nil {
		t.Fatal(err)
	}
	ins := ir.Instruction{Op: ir.OpVShuffle, A: 7, B: 11}
	if got := ins.Inputs(); !slices.Equal(got, []int{7, 11}) {
		t.Fatalf("Inputs=%v", got)
	}
}

func TestExtendValidation(t *testing.T) {
	for _, op := range []ir.Opcode{ir.OpVExtendUnsigned, ir.OpVExtendSigned} {
		for _, test := range []struct {
			name     string
			from, to ir.Type
			imm      uint64
			valid    bool
		}{
			{"whole", ir.Vec(16, 8), ir.Vec(16, 16), 0, true},
			{"small", ir.Vec(2, 8), ir.Vec(2, 16), 0, true},
			{"largest", ir.Vec(8, 32), ir.Vec(8, 64), 0, true},
			{"low-half", ir.Vec(16, 8), ir.Vec(8, 16), 0, true},
			{"high-half", ir.Vec(4, 32), ir.Vec(2, 64), 1, true},
			{"whole-imm", ir.Vec(8, 16), ir.Vec(8, 32), 1, false},
			{"half-imm", ir.Vec(16, 8), ir.Vec(8, 16), 2, false},
			{"same-width", ir.Vec(8, 16), ir.Vec(8, 16), 0, false},
			{"quadruple", ir.Vec(8, 8), ir.Vec(8, 32), 0, false},
			{"lanes", ir.Vec(8, 8), ir.Vec(2, 16), 0, false},
			{"scalar", ir.Scalar(32), ir.Vec(2, 32), 0, false},
		} {
			t.Run(op.String()+"/"+test.name, func(t *testing.T) {
				err := extendProgram(test.from, test.to, op, test.imm).Validate()
				if test.valid && err != nil || !test.valid && !errors.Is(err, ir.ErrInvalidProgram) {
					t.Fatalf("Validate=%v; want valid=%v", err, test.valid)
				}
			})
		}
		p := extendProgram(ir.Vec(4, 16), ir.Vec(4, 32), op, 0)
		p.Instructions[1].Data = []byte{0}
		if err := p.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
			t.Fatalf("%v accepted Data: %v", op, err)
		}
	}
}
