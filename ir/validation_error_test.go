package ir_test

import (
	"errors"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestValidationErrorLocatesRule(t *testing.T) {
	param := ir.Instruction{Op: ir.OpParam}
	cases := []struct {
		name    string
		program func(t *testing.T) *ir.Program
		index   int
		op      ir.Opcode
		rule    string
	}{
		{"operand", func(*testing.T) *ir.Program {
			return &ir.Program{Arity: 1, Result: 1, Instructions: []ir.Instruction{param, {Op: ir.OpAdd, A: 0, B: 5}}}
		}, 1, ir.OpAdd, "uses non-value 5"},
		{"width", func(*testing.T) *ir.Program {
			return &ir.Program{Arity: 1, Result: 2, Instructions: []ir.Instruction{param, {Op: ir.OpConst, Bits: 32}, {Op: ir.OpAdd, A: 0, B: 1}}}
		}, 2, ir.OpAdd, "needs 64-bit operands"},
		{"field", func(*testing.T) *ir.Program {
			return &ir.Program{Arity: 1, Result: 1, Instructions: []ir.Instruction{param, {Op: ir.OpAdd, A: 0, B: 0, Symbol: "x"}}}
		}, 1, ir.OpAdd, "unused or invalid Symbol"},
		{"vector", func(*testing.T) *ir.Program {
			v := ir.Vec(4, 32)
			return &ir.Program{Arity: 1, Result: 0, Instructions: []ir.Instruction{param, {Op: ir.OpVZero, Type: v, Bits: 64}}}
		}, 1, ir.OpVZero, "Bits=64"},
		{"predicate", func(t *testing.T) *ir.Program {
			b := n.NewBuilder()
			x := b.ParamNative()
			yes, no := b.NewLabel(), b.NewLabel()
			b.Branch(b.Compare(x, ir.BranchEqual, b.ConstNative(1)), yes)
			b.Jump(no)
			b.Bind(yes)
			b.Return(b.ConstNative(1))
			b.Bind(no)
			b.Return(b.ConstNative(2))
			p := finish(t, b)
			for i := range p.Control.Steps {
				if pred := p.Control.Steps[i].Predicate; pred != nil {
					pred.Value = len(p.Instructions) + 1
				}
			}
			return p
		}, -1, 0, "predicate node 0"},
		{"variable", func(t *testing.T) *ir.Program {
			p := variableProgram(t)
			p.Instructions[p.Result].Variable = 1
			return p
		}, -2, ir.OpGet, "Var 1"},
		{"control step", func(*testing.T) *ir.Program {
			return &ir.Program{
				Instructions: []ir.Instruction{{Op: ir.OpLabelParam, Bits: 64}},
				Result:       -1,
				Control: &ir.Control{
					Labels: []bool{true}, LabelScopes: []int{0}, Params: []int{1}, ParamBits: [][]uint8{{64}},
					Steps: []ir.ControlStep{{IsLabel: true, Label: 0}, {Term: ir.TermReturn, Start: 0, End: 1, Result: 0}},
				},
			}
		}, -1, 0, "entry label 0"},
		{"label parameter", func(*testing.T) *ir.Program {
			return &ir.Program{
				Instructions: []ir.Instruction{{Op: ir.OpLabelParam, Bits: 64, Imm: 3}},
				Result:       -1,
				Control: &ir.Control{
					Labels: []bool{true}, LabelScopes: []int{0}, Params: []int{1}, ParamBits: [][]uint8{{64}},
					Steps: []ir.ControlStep{{IsLabel: true, Label: 0}, {Term: ir.TermReturn, Start: 0, End: 1, Result: 0}},
				},
			}
		}, 0, ir.OpLabelParam, "label parameter index 3"},
		{"call effect", func(*testing.T) *ir.Program {
			call := ir.Instruction{Op: ir.OpCallC, Args: []int{0}, Effect: &abi.CallEffect{Kind: abi.SystemCall}}
			return &ir.Program{Arity: 1, Result: 1, Instructions: []ir.Instruction{param, call}}
		}, 1, ir.OpCallC, "must describe a function call"},
	}
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := c.program(t)
			index := c.index
			if index == -2 {
				index = p.Result
			}
			err := p.Validate()
			var invalid *ir.ValidationError
			if !errors.As(err, &invalid) || !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatalf("Validate = %v, want a ValidationError", err)
			}
			if invalid.Index != index || index >= 0 && invalid.Op != c.op || !strings.Contains(invalid.Rule, c.rule) {
				t.Fatalf("got Index %d Op %s Rule %q, want Index %d Op %s Rule containing %q", invalid.Index, invalid.Op, invalid.Rule, index, c.op, c.rule)
			}
			if err.Error() != invalid.Error() || !strings.HasSuffix(err.Error(), ": ir: invalid program") {
				t.Fatalf("Error() = %q", err.Error())
			}
			if _, err := ir.Seal(p); !errors.As(err, &invalid) {
				t.Fatalf("Seal = %v, want a ValidationError", err)
			}
			_, err = compile.Lower(target, p, compile.Func{ABI: abi.C})
			if !errors.Is(err, compile.ErrInvalidFunction) || !errors.As(err, &invalid) || invalid.Index != index {
				t.Fatalf("Lower = %v, want ErrInvalidFunction and the ValidationError", err)
			}
		})
	}
}

func TestValidationErrorText(t *testing.T) {
	located := &ir.ValidationError{Index: 12, Op: ir.OpAdd, Rule: "uses non-value 20"}
	if got, want := located.Error(), "instruction 12 (OpAdd): uses non-value 20: ir: invalid program"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	general := &ir.ValidationError{Index: -1, Rule: "label 3 has 1 parameters but no incoming edge"}
	if got, want := general.Error(), "label 3 has 1 parameters but no incoming edge: ir: invalid program"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(general, ir.ErrInvalidProgram) {
		t.Fatal("ValidationError does not wrap ErrInvalidProgram")
	}
}

func finish(t *testing.T, b *n.Builder) *ir.Program {
	t.Helper()
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return p.Clone()
}
