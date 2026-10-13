package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

// TestDivisionSemanticsDefaults checks that each division records the
// builder's semantics when it is built, and that the With forms override it.
func TestDivisionSemanticsDefaults(t *testing.T) {
	b := NewBuilder()
	x, _ := b.ParamBits(32)
	y, _ := b.ParamBits(32)
	first, err := b.Div(x, y)
	if err != nil {
		t.Fatal(err)
	}
	b.SetDivisionSemantics(ir.DivisionAllOnes)
	second, _ := b.RemSigned(first, y)
	third, _ := b.DivSignedWith(ir.DivisionUnchecked, second, y)
	if err := b.Return(third); err != nil {
		t.Fatal(err)
	}
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var got []ir.Instruction
	for _, ins := range p.Instructions() {
		switch ins.Op {
		case ir.OpDiv, ir.OpRemSigned, ir.OpDivSigned:
			got = append(got, ins)
		}
	}
	want := []struct {
		op ir.Opcode
		s  ir.DivisionSemantics
	}{{ir.OpDiv, ir.DivisionZero}, {ir.OpRemSigned, ir.DivisionAllOnes}, {ir.OpDivSigned, ir.DivisionUnchecked}}
	if len(got) != len(want) {
		t.Fatalf("got %d divisions, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Op != w.op || ir.DivisionSemantics(got[i].Imm) != w.s || got[i].Bits != 32 {
			t.Errorf("division %d = %v Imm %d Bits %d, want %v Imm %d Bits 32", i, got[i].Op, got[i].Imm, got[i].Bits, w.op, w.s)
		}
	}
}

func TestDivisionRejectsInvalidOperands(t *testing.T) {
	b := NewBuilder()
	x, _ := b.ParamBits(32)
	y, _ := b.ParamBits(64)
	if _, err := b.Div(x, y); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("mixed widths: %v", err)
	}
	if _, err := b.DivWith(ir.DivisionSemantics(9), x, x); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("undefined semantics: %v", err)
	}
	lo, _ := b.ParamBits(64)
	hi, _ := b.ParamBits(64)
	wide, err := b.Concat(lo, hi)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Rem(wide, wide); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("128-bit division: %v", err)
	}
}

func TestConversionSemanticsAndWidths(t *testing.T) {
	b := NewBuilder()
	x, _ := b.ParamBits(64)
	f, err := b.FFromSigned(x, 32)
	if err != nil {
		t.Fatal(err)
	}
	b.SetConversionSemantics(ir.ConversionNative)
	n, err := b.FToUnsigned(f, 64)
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.FToSignedWith(ir.ConversionSaturate, f, 32)
	if err != nil {
		t.Fatal(err)
	}
	wide, _ := b.CastUnsigned(s, 64)
	if err := b.Return(b.mustXor(t, n, wide)); err != nil {
		t.Fatal(err)
	}
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[ir.Opcode]ir.Instruction{}
	for _, ins := range p.Instructions() {
		seen[ins.Op] = ins
	}
	if ins := seen[ir.OpFFromSigned]; ins.Bits != 32 {
		t.Errorf("FFromSigned Bits = %d, want 32", ins.Bits)
	}
	if ins := seen[ir.OpFToUnsigned]; ins.Bits != 64 || ir.ConversionSemantics(ins.Imm) != ir.ConversionNative {
		t.Errorf("FToUnsigned = Bits %d Imm %d, want 64 native", ins.Bits, ins.Imm)
	}
	if ins := seen[ir.OpFToSigned]; ins.Bits != 32 || ir.ConversionSemantics(ins.Imm) != ir.ConversionSaturate {
		t.Errorf("FToSigned = Bits %d Imm %d, want 32 saturate", ins.Bits, ins.Imm)
	}
	b = NewBuilder()
	small, _ := b.ParamBits(16)
	if _, err := b.FFromUnsigned(small, 64); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("16-bit source: %v", err)
	}
	g, _ := b.ParamBits(64)
	if _, err := b.FToSigned(g, 16); !errors.Is(err, ErrInvalidFunction) {
		t.Errorf("16-bit result: %v", err)
	}
}

func (b *Builder) mustXor(t *testing.T, x, y Value) Value {
	t.Helper()
	v, err := b.Xor(x, y)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
