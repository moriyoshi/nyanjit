package main

import (
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
	"slices"
)

// translateIR describes the same bytecode with ir.ProgramBuilder instead of
// regalloc.FunctionBuilder. Each block's parameters carry its locals; edges
// pass the current values, as an SSA front end passes phi operands.
func translateIR(p *Program, bits uint8) (*ir.SealedProgram, error) {
	if _, err := p.check(); err != nil {
		return nil, err
	}
	word := ir.Scalar(bits)
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	locals := make([]int, p.Locals)
	for i := 0; i < p.Params; i++ {
		locals[i] = pb.Param(entry, word)
	}
	zero := pb.Append(entry, ir.Instruction{Op: ir.OpConst, Bits: bits})
	for i := p.Params; i < p.Locals; i++ {
		locals[i] = zero
	}
	// Use a synthetic entry even when bytecode jumps back to instruction zero.
	// Function arguments are entry definitions; loop locals are block parameters.
	leader := make([]bool, len(p.Code)+1)
	leader[0] = true
	for pc, in := range p.Code {
		switch in.Op {
		case Jmp, Jz:
			leader[in.Arg], leader[pc+1] = true, true
		case Ret:
			leader[pc+1] = true
		}
	}
	localTypes := make([]ir.Type, p.Locals)
	for i := range localTypes {
		localTypes[i] = word
	}
	blocks := make([]ir.Block, len(p.Code))
	for pc := range p.Code {
		if leader[pc] {
			blocks[pc] = pb.NewBlock(localTypes...)
		}
	}
	pb.Jump(entry, blocks[0], slices.Clone(locals)...)
	block := entry
	var values []int
	pop := func() int {
		v := values[len(values)-1]
		values = values[:len(values)-1]
		return v
	}
	open := false
	for pc, in := range p.Code {
		if leader[pc] {
			if open {
				pb.Jump(block, blocks[pc], slices.Clone(locals)...)
			}
			block, open = blocks[pc], true
			for i := range locals {
				locals[i] = pb.BlockParam(block, i)
			}
		}
		switch in.Op {
		case Push:
			values = append(values, pb.Append(block, ir.Instruction{Op: ir.OpConst, Bits: bits, Imm: uint64(in.Arg)}))
		case Get:
			values = append(values, locals[in.Arg])
		case Set:
			locals[in.Arg] = pop()
		case Dup:
			values = append(values, values[len(values)-1])
		case Drop:
			pop()
		case Add, Sub, Mul, Div, And, Or, Xor:
			b, a := pop(), pop()
			values = append(values, pb.Append(block, ir.Instruction{Op: arithmetic[in.Op], Bits: bits, A: a, B: b}))
		case Jmp:
			pb.Jump(block, blocks[in.Arg], slices.Clone(locals)...)
			open = false
		case Jz:
			pred := &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: pop()}
			pb.Branch(block, pred, blocks[in.Arg], slices.Clone(locals), blocks[pc+1], slices.Clone(locals))
			open = false
		case Ret:
			pb.Return(block, pop())
			open = false
		}
	}
	return pb.Seal()
}

type irCompiler struct{}

func (irCompiler) Compile(p *Program, t *target, listing bool) (*Function, error) {
	program, err := translateIR(p, t.policy.RegisterBits())
	if err != nil {
		return nil, err
	}
	target, err := compile.NewTarget(compile.TargetSpec{Architecture: t.arch})
	if err != nil {
		return nil, err
	}
	u, err := compile.Lower(target, program, compile.Func{
		ABI: abi.C, Context: compile.ContextExternal,
		Stack: compile.StackPolicy{Practice: stack, BytesAvailable: 4096},
	})
	if err != nil {
		return nil, err
	}
	f := &Function{FrameBytes: u.Report().StackBytes()}
	if listing {
		f.Listing, err = u.Listing()
	} else {
		tpl, e := u.Template()
		if e != nil {
			return nil, e
		}
		f.Code, err = tpl.Bytes(nil)
	}
	return f, err
}
