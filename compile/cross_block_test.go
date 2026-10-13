package compile_test

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// outerChain defines values entry values and uses all of them after a
// chain of blocks, so that each value is live through every block.
func outerChain(values, blocks int) *ir.SealedProgram {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	x := pb.Param(entry, ir.Scalar(64))
	outer := make([]int, values)
	for i := range outer {
		c := pb.Append(entry, ir.Instruction{Op: ir.OpConst, Imm: uint64(i)})
		outer[i] = pb.Append(entry, ir.Instruction{Op: ir.OpAdd, A: x, B: c})
	}
	from := entry
	for range blocks {
		next := pb.NewBlock()
		pb.Jump(from, next)
		from = next
	}
	acc := x
	for _, v := range outer {
		acc = pb.Append(from, ir.Instruction{Op: ir.OpXor, A: acc, B: v})
	}
	pb.Return(from, acc)
	program, err := pb.Seal()
	if err != nil {
		panic(err)
	}
	return program
}

func TestCrossBlockThreadingBudget(t *testing.T) {
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = compile.Lower(target, outerChain(400, 400), compile.Func{ABI: abi.C})
	var limit *compile.LimitError
	if !errors.As(err, &limit) || limit.Kind != compile.LimitArguments || limit.Needed <= limit.Limit {
		t.Fatalf("Lower = %v, want a LimitError for threaded values", err)
	}
	// Four 64-bit values fit 386's spill budget, where every 64-bit label
	// parameter takes two reserved words in every block.
	if _, err := compile.Lower(target, outerChain(4, 400), compile.Func{ABI: abi.C}); err != nil {
		t.Fatalf("a long chain with few live values: %v", err)
	}
}

func TestCrossBlockManyValuesIntoOneBlock(t *testing.T) {
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = compile.Lower(target, outerChain(5000, 1), compile.Func{ABI: abi.C})
	if err != nil && (!errors.Is(err, compile.ErrBackendLimit) || errors.Is(err, compile.ErrInvalidFunction)) {
		t.Fatalf("Lower = %v, want success or a backend limit", err)
	}
	t.Logf("5000 values into one block: %v", err)
}
