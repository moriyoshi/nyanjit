package compile_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSIMDScalarPredicateValidation(t *testing.T) {
	b := nyanjit.NewBuilder()
	a := b.ParamNative()
	v := b.VLoad(nyanjit.Vec(2, 64), a)
	_ = b.VCompare(v, nyanjit.BranchEqual, v)
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(b.Compare(a, nyanjit.BranchEqual, a), yes, no)
	b.Bind(yes)
	b.Return(b.Literal(1))
	b.Bind(no)
	b.Return(b.Literal(0))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for id, ins := range p.Instructions {
		if ins.Type.Kind != ir.VectorKind && ins.Type.Kind != ir.MaskKind {
			continue
		}
		for _, operand := range []string{"left", "right", "immediate", "anchor"} {
			q := p.Clone()
			for i := range q.Control.Steps {
				s := &q.Control.Steps[i]
				if s.Predicate == nil {
					continue
				}
				switch operand {
				case "left":
					s.Predicate.Value = id
				case "right":
					s.Predicate.Other = id
				case "immediate":
					s.Predicate.Value = id
					s.Predicate.WithValue = false
				case "anchor":
					s.Predicate.Anchor = id
					s.Predicate.HasAnchor = true
				}
			}
			if err := q.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatalf("%s type%+v accepted: %v", operand, ins.Type, err)
			}
		}
	}
}

func TestSIMDInstructionBitsMustMatchShape(t *testing.T) {
	b := nyanjit.NewBuilder()
	a := b.ParamNative()
	s := b.ConstBits(32, 3)
	typ := nyanjit.Vec(4, 32)
	x := b.VLoad(typ, a)
	y := b.Splat(typ, s)
	_ = b.ConstVector(typ, make([]byte, 16))
	for _, v := range []nyanjit.Value{b.Add(x, y), b.Sub(x, y), b.Mul(x, y), b.And(x, y), b.Or(x, y), b.Xor(x, y), b.ShiftLeft(x, 1), b.ShiftRight(x, 1), b.ShiftRightArithmetic(x, 1), b.RotateLeft(x, 1), b.RotateRight(x, 1), b.ShiftLeftBy(x, s), b.ShiftRightBy(x, s), b.ShiftRightArithmeticBy(x, s), b.RotateLeftBy(x, s), b.RotateRightBy(x, s), b.InsertLane(x, 1, s)} {
		b.VStore(a, v)
	}
	b.VStore(a, x)
	_ = b.ExtractLane(x, 1)
	b.Return(b.MaskBits(b.VCompare(x, nyanjit.BranchEqual, y)))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for id, ins := range p.Instructions {
		if !ins.IsVector() {
			continue
		}
		q := p.Clone()
		q.Instructions[id].Type = ins.ValueType()
		q.Instructions[id].Bits = 16
		if ins.Bits == 16 {
			q.Instructions[id].Bits = 8
		}
		if err := q.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
			t.Fatalf("op%d value%d accepted mismatched Bits: %v", ins.Op, id, err)
		}
	}
}

func TestSIMDRootErrorsNameOperation(t *testing.T) {
	for _, op := range []string{"VStore", "VCompare"} {
		b := nyanjit.NewBuilder()
		p := b.Param()
		switch op {
		case "VStore":
			b.VStore(p, p)
		case "VCompare":
			b.VCompare(p, nyanjit.BranchEqual, p)
		}
		_, err := b.Finish()
		if !errors.Is(err, nyanjit.ErrInvalidFunction) || !strings.HasPrefix(err.Error(), op+":") {
			t.Fatalf("%s: %v", op, err)
		}
	}
}

func TestSIMDZeroInsertAndLabelBitsValidation(t *testing.T) {
	typ := ir.Vec(4, 32)
	p := &ir.Program{Arity: 1, Result: 5, Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 64}, {Op: ir.OpConst, Bits: 32},
		{Op: ir.OpVZero, Type: typ, Bits: 32},
		{Op: ir.OpVInsertZero, Type: typ, Bits: 32, A: 2, B: 1},
		{Op: ir.OpVStore, Type: typ, Bits: 32, A: 0, B: 3},
		{Op: ir.OpConst, Bits: 64},
	}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{2, 3} {
		q := p.Clone()
		q.Instructions[id].Bits = 8
		if err := q.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
			t.Fatalf("operation%d accepted wrong lane width", q.Instructions[id].Op)
		}
	}
	b := nyanjit.NewBuilder()
	a := b.ParamNative()
	v := b.VLoad(typ, a)
	m := b.VCompare(v, nyanjit.BranchEqual, v)
	label := b.NewLabel()
	b.JumpArgs(label, v, m.Value())
	ps := b.BindParamsTypes(label, typ, nyanjit.Mask(4))
	b.Return(b.MaskBits(ps[1].Mask()))
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	for id, i := range p.Instructions {
		if i.Op != ir.OpLabelParam {
			continue
		}
		q := p.Clone()
		q.Instructions[id].Bits = 8
		if err := q.Validate(); !errors.Is(err, ir.ErrInvalidProgram) {
			t.Fatalf("typed label %+v accepted wrong Bits", i.Type)
		}
	}
}
