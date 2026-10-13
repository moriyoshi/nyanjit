package compile_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestDeferredExpressionPreservesScaledDisplacementFusion(t *testing.T) {
	for _, form := range []string{"constant multiply", "literal multiply"} {
		t.Run(form, func(t *testing.T) {
			b := builder.NewBuilder()
			x, err := b.Param()
			if err != nil {
				t.Fatal(err)
			}
			y, err := b.Param()
			if err != nil {
				t.Fatal(err)
			}
			displacement, err := b.Literal(16)
			if err != nil {
				t.Fatal(err)
			}
			address := x.Add(y.MulConst(8))
			if form == "literal multiply" {
				scale, err := b.Literal(8)
				if err != nil {
					t.Fatal(err)
				}
				address = x.Add(y.Mul(scale))
			}
			if err := b.Return(address.Add(displacement)); err != nil {
				t.Fatal(err)
			}
			program, err := testFinishRaw(b)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := compile.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
				program.Result, nil, abi.MaxArguments(), amd64.NewPolicy(abi.C, abi.StackNoUse))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, ins := range selected.Instructions {
				if ins.Op == machine.SelectedScaled {
					count++
					if ins.Imm != 8 || ins.Disp != 16 {
						t.Fatalf("scaled instruction = %+v, want scale 8 and displacement 16", ins)
					}
				}
			}
			if count != 1 {
				t.Fatalf("got %d scaled instructions, want one: %+v", count, selected.Instructions)
			}
		})
	}
}

func TestPlainAdditionCanFoldAsScaleOneAddress(t *testing.T) {
	b := builder.NewBuilder()
	base, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	index, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.Load64(base.Add(index))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(value); err != nil {
		t.Fatal(err)
	}
	program, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := compile.SelectProgram(program.Instructions, program.ScopeExits, program.Arity,
		program.Result, nil, abi.MaxArguments(), amd64.NewPolicy(abi.C, abi.StackNoUse))
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range selected.Instructions {
		if ins.Op == machine.SelectedLoadAddress && ins.Imm == 1 && ins.Disp == 0 {
			return
		}
	}
	t.Fatalf("plain addition was not folded into memory address: %+v", selected.Instructions)
}
