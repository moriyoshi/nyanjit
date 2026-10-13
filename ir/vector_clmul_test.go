package ir_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func clmulProgram(typ ir.Type) *ir.Program {
	return &ir.Program{Result: 3, Instructions: []ir.Instruction{
		{Op: ir.OpVZero, Type: typ, Bits: typ.LaneBits},
		{Op: ir.OpVZero, Type: typ, Bits: typ.LaneBits},
		{Op: ir.OpVCarrylessMul, Type: typ, Bits: typ.LaneBits, A: 0, B: 1},
		{Op: ir.OpConst, Bits: 32},
	}}
}

func TestCarrylessMulValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ir.Program)
	}{
		{"valid", func(*ir.Program) {}},
		{"lane-width", func(p *ir.Program) {
			for i := 0; i < 3; i++ {
				p.Instructions[i].Type = ir.Vec(4, 32)
				p.Instructions[i].Bits = 32
			}
		}},
		{"odd-lanes", func(p *ir.Program) {
			for i := 0; i < 3; i++ {
				p.Instructions[i].Type = ir.Vec(3, 64)
			}
		}},
		{"input-A", func(p *ir.Program) { p.Instructions[0].Type = ir.Vec(4, 64) }},
		{"input-B", func(p *ir.Program) { p.Instructions[1].Type = ir.Vec(4, 64) }},
		{"equal-size-different-lanes", func(p *ir.Program) { p.Instructions[1].Type = ir.Vec(4, 32); p.Instructions[1].Bits = 32 }},
		{"selector", func(p *ir.Program) { p.Instructions[2].Imm = 4 }},
		{"bits", func(p *ir.Program) { p.Instructions[2].Bits = 32 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := clmulProgram(ir.Vec(2, 64))
			test.change(p)
			err := p.Validate()
			if test.name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatalf("Validate=%v; want ErrInvalidProgram", err)
			}
		})
	}
	for selector := uint64(0); selector < 4; selector++ {
		p := clmulProgram(ir.Vec(8, 64))
		p.Instructions[2].Imm = selector
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	ins := ir.Instruction{Op: ir.OpVCarrylessMul, A: 7, B: 11}
	if got := ins.Inputs(); !slices.Equal(got, []int{7, 11}) {
		t.Fatalf("Inputs=%v", got)
	}
}
