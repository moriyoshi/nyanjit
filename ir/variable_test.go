package ir_test

import (
	"errors"
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func variableProgram(t *testing.T) *ir.Program {
	b := n.NewBuilder()
	outer := b.Var(b.ConstBits(32, 1))
	b.Enter()
	inner := b.Var(b.ConstBits(32, 2))
	_ = b.Get(inner)
	b.Leave()
	b.Return(b.Get(outer))
	p, e := b.Finish()
	if e != nil {
		t.Fatal(e)
	}
	return p.Clone()
}
func TestVariableIRValidation(t *testing.T) {
	for _, modify := range []func(*ir.Program){
		func(p *ir.Program) { p.Instructions[p.Result].Variable = 1 },
		func(p *ir.Program) { p.Variables[0].Region = 999 },
		func(p *ir.Program) { p.Variables[0].Initial[0] = p.Variables[0].Declaration },
		func(p *ir.Program) { p.Instructions[p.Result].Bits = 64 },
		func(p *ir.Program) { p.Instructions[p.Result].Variable = -1 },
		func(p *ir.Program) { p.Instructions[p.Variables[0].Declaration].Type = ir.Scalar(64) },
	} {
		p := variableProgram(t)
		modify(p)
		if e := p.Validate(); !errors.Is(e, ir.ErrInvalidProgram) {
			t.Fatalf("malformed vars accepted:%v", e)
		}
	}
}
func TestCaptureInitializationCannotBeRemoved(t *testing.T) {
	b := n.NewBuilder()
	x := b.ParamNative()
	b.Enter()
	l := b.NewLabel()
	b.Jump(l)
	b.Bind(l)
	y := b.Add(x, b.ConstNative(1))
	b.Leave(y)
	b.Return(y)
	sealed, e := b.Finish()
	if e != nil {
		t.Fatal(e)
	}
	p := sealed.Clone()
	p.Instructions[p.Variables[0].Declaration].Args = nil
	if e := p.Validate(); !errors.Is(e, ir.ErrInvalidProgram) {
		t.Fatalf("missing capture initializer:%v", e)
	}
}
