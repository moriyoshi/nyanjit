package ir_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

// sumBelow builds sum(0..n-1) in SSA style, with the loop counter and the
// accumulator as block parameters and n used from the entry block directly.
func sumBelow() (*ir.ProgramBuilder, int) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	n := pb.Param(entry, ir.Scalar(64))
	loop := pb.NewBlock(ir.Scalar(64), ir.Scalar(64))
	body, done := pb.NewBlock(), pb.NewBlock()
	zero := pb.Append(entry, ir.Instruction{Op: ir.OpConst})
	pb.Jump(entry, loop, zero, zero)
	i, acc := pb.BlockParam(loop, 0), pb.BlockParam(loop, 1)
	pb.Branch(loop, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchBelow, Value: i, Other: n, WithValue: true},
		body, nil, done, nil)
	one := pb.Append(body, ir.Instruction{Op: ir.OpConst, Imm: 1})
	next := pb.Append(body, ir.Instruction{Op: ir.OpAdd, A: i, B: one})
	sum := pb.Append(body, ir.Instruction{Op: ir.OpAdd, A: acc, B: i})
	pb.Jump(body, loop, next, sum)
	pb.Return(done, acc)
	return pb, sum
}

func TestProgramBuilderSealsSSAProgram(t *testing.T) {
	pb, _ := sumBelow()
	program, err := pb.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if program.Arity() != 1 || program.Control() == nil {
		t.Fatalf("arity %d, control %v", program.Arity(), program.Control())
	}
	// Reverse postorder puts the entry first, and the loop header before the
	// blocks it dominates.
	a := program.Analysis()
	steps := program.Control().Steps
	header := steps[3].Start
	for _, step := range steps[4:] {
		if !step.IsLabel && step.Start < step.End && !a.Dominates(header, step.Start) {
			t.Fatalf("loop header %d does not dominate %d", header, step.Start)
		}
	}
	if _, err := pb.Seal(); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("second Seal: %v", err)
	}
}

func TestProgramBuilderRejectsNonDominatingUse(t *testing.T) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	x := pb.Param(entry, ir.Scalar(64))
	left, right, join := pb.NewBlock(), pb.NewBlock(), pb.NewBlock()
	pb.Branch(entry, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: x}, left, nil, right, nil)
	y := pb.Append(left, ir.Instruction{Op: ir.OpAdd, A: x, B: x})
	pb.Jump(left, join)
	pb.Jump(right, join)
	use := pb.Append(join, ir.Instruction{Op: ir.OpXor, A: y, B: x})
	pb.Return(join, use)
	_, err := pb.Seal()
	var invalid *ir.ValidationError
	if !errors.As(err, &invalid) || invalid.Index != use || invalid.Op != ir.OpXor {
		t.Fatalf("Seal error = %v, want a ValidationError at the builder's ID %d", err, use)
	}
}

func TestProgramBuilderErrors(t *testing.T) {
	for name, build := range map[string]func(pb *ir.ProgramBuilder){
		"unterminated": func(pb *ir.ProgramBuilder) {
			pb.Jump(pb.Entry(), pb.NewBlock())
		},
		"argument count": func(pb *ir.ProgramBuilder) {
			pb.Jump(pb.Entry(), pb.NewBlock(ir.Scalar(64)))
		},
		"non-value operand": func(pb *ir.ProgramBuilder) {
			x := pb.Param(pb.Entry(), ir.Scalar(64))
			store := pb.Append(pb.Entry(), ir.Instruction{Op: ir.OpStore64, A: x, B: x})
			pb.Return(pb.Entry(), store)
		},
		"param outside entry": func(pb *ir.ProgramBuilder) {
			b := pb.NewBlock()
			pb.Param(b, ir.Scalar(64))
		},
		"terminated twice": func(pb *ir.ProgramBuilder) {
			x := pb.Param(pb.Entry(), ir.Scalar(64))
			pb.Return(pb.Entry(), x)
			pb.Return(pb.Entry(), x)
		},
		"value from unreachable block": func(pb *ir.ProgramBuilder) {
			dead := pb.NewBlock()
			v := pb.Append(dead, ir.Instruction{Op: ir.OpConst})
			pb.Return(dead, v)
			pb.Return(pb.Entry(), v)
		},
		"Append of a parameter": func(pb *ir.ProgramBuilder) {
			pb.Append(pb.Entry(), ir.Instruction{Op: ir.OpParam})
		},
	} {
		t.Run(name, func(t *testing.T) {
			pb := ir.NewProgramBuilder()
			build(pb)
			if _, err := pb.Seal(); !errors.Is(err, ir.ErrInvalidProgram) {
				t.Fatalf("Seal: %v, want ErrInvalidProgram", err)
			}
		})
	}
}

func TestProgramBuilderDropsUnreachableBlocks(t *testing.T) {
	pb := ir.NewProgramBuilder()
	x := pb.Param(pb.Entry(), ir.Scalar(64))
	dead := pb.NewBlock()
	pb.Append(dead, ir.Instruction{Op: ir.OpConst, Imm: 7})
	pb.Return(pb.Entry(), x)
	program, err := pb.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if program.Len() != 1 || len(program.Control().Labels) != 1 {
		t.Fatalf("unreachable block kept: %d instructions, %d labels", program.Len(), len(program.Control().Labels))
	}
}

// TestProgramBuilderReadmeExample keeps the README's snippet valid.
func TestProgramBuilderReadmeExample(t *testing.T) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	n := pb.Param(entry, ir.Scalar(64))
	loop := pb.NewBlock(ir.Scalar(64)) // one block parameter
	done := pb.NewBlock()
	pb.Jump(entry, loop, n)
	i := pb.BlockParam(loop, 0)
	one := pb.Append(loop, ir.Instruction{Op: ir.OpConst, Imm: 1})
	next := pb.Append(loop, ir.Instruction{Op: ir.OpSub, A: i, B: one})
	pb.Branch(loop, &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: next},
		done, nil, loop, []int{next})
	pb.Return(done, n) // n is defined in the entry block, which dominates done
	if _, err := pb.Seal(); err != nil {
		t.Fatal(err)
	}
}

// A sealed program owns its storage: reusing a buffer passed to Append after
// Seal must not change it (security audit 2026-10-25, L14).
func TestProgramBuilderSealCopiesData(t *testing.T) {
	pb := ir.NewProgramBuilder()
	e := pb.Entry()
	buf := make([]byte, 16)
	buf[0] = 1
	v := pb.Append(e, ir.Instruction{Op: ir.OpVConst, Type: ir.Vec(2, 64), Bits: 64, Data: buf})
	x := pb.Append(e, ir.Instruction{Op: ir.OpVExtract, Type: ir.Scalar(64), Bits: 64, A: v, Imm: 0})
	pb.Return(e, x)
	s, err := pb.Seal()
	if err != nil {
		t.Fatal(err)
	}
	buf[0] = 0x42
	for _, ins := range s.Instructions() {
		if ins.Op == ir.OpVConst && ins.Data[0] != 1 {
			t.Fatalf("sealed VConst changed with the caller's buffer: %#x", ins.Data[0])
		}
	}
}
