package ir_test

import (
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func TestNarrowMemoryValidation(t *testing.T) {
	for _, bits := range []uint8{16, 32} {
		load, store := ir.OpLoad16, ir.OpStore16
		if bits == 32 {
			load, store = ir.OpLoad32, ir.OpStore32
		}
		original := &ir.Program{Arity: 2, Result: 3, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 32}, {Op: ir.OpParam, Bits: 64}, {Op: store, Bits: bits, A: 0, B: 1, Aligned: true}, {Op: load, Bits: bits, A: 0, Aligned: true}}}
		if e := original.Validate(); e != nil {
			t.Fatal(e)
		}
		if got := original.Instructions[3].ValueType(); got != ir.Scalar(bits) {
			t.Fatal(got)
		}
		for _, mutate := range []func(*ir.Program){
			func(p *ir.Program) { p.Instructions[3].Bits = 64 },
			func(p *ir.Program) { p.Instructions[3].ByteOrder = 99 },
			func(p *ir.Program) { p.Instructions[0].Bits = 16 },
			func(p *ir.Program) { p.Instructions[1].Bits = 8 },
			func(p *ir.Program) { p.Instructions[2].B = 3 },
			func(p *ir.Program) { p.Instructions[0].Aligned = true },
		} {
			p := original.Clone()
			mutate(p)
			if e := p.Validate(); e == nil {
				t.Fatalf("accepted malformed narrow access: %+v", p)
			}
		}
		if got := original.Instructions[2].Inputs(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
			t.Fatal(got)
		}
		p := original.Clone()
		p.Result = 2
		if e := p.Validate(); e == nil {
			t.Fatal("store defines value")
		}
	}
}
