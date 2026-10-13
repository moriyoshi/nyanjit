//go:build linux && 386

package compile

import (
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/ir"
)

// These tests lower raw-IR block parameters, which Builders no longer
// produce, on 386, where a 64-bit parameter takes a register pair.

func compileLabel386(t *testing.T, pb *ir.ProgramBuilder) uint64 {
	t.Helper()
	program, err := pb.Seal()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compileCode(program, TargetSpec{}, Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	lo, hi, err := code.CallPair()
	if err != nil {
		t.Fatal(err)
	}
	return execution.Wide(lo, hi)
}

func labelConst386(pb *ir.ProgramBuilder, block ir.Block, bits uint8, value uint64) int {
	return pb.Append(block, ir.Instruction{Op: ir.OpConst, Bits: bits, Imm: value})
}

func labelIsZero386(value int) *ir.PredicateExpr {
	return &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: value}
}

func labelParams386(pb *ir.ProgramBuilder, block ir.Block, n int) []int {
	values := make([]int, n)
	for i := range values {
		values[i] = pb.BlockParam(block, i)
	}
	return values
}

func TestLabelArgumentsLoop386(t *testing.T) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	loop, done := pb.NewBlock(ir.Scalar(32)), pb.NewBlock(ir.Scalar(32))
	pb.Jump(entry, loop, labelConst386(pb, entry, 32, 5))
	value := pb.BlockParam(loop, 0)
	next := pb.Append(loop, ir.Instruction{Op: ir.OpSub, Bits: 32, A: value, B: labelConst386(pb, loop, 32, 1)})
	nonzero := &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchNotEqual, Value: next}
	pb.Branch(loop, nonzero, loop, []int{next}, done, []int{next})
	pb.Return(done, pb.BlockParam(done, 0))
	if got := compileLabel386(t, pb); got != 0 {
		t.Fatalf("loop result = %d, want 0", got)
	}
}

func TestLabelArgumentsParallelCopies386(t *testing.T) {
	for _, tc := range []struct {
		count int
		cycle bool
		want  uint64
	}{
		{count: 3, want: 2211},
		{count: 4, cycle: true, want: 311},
		{count: 8, want: 2211},
		{count: 9, cycle: true, want: 311},
	} {
		t.Run(strconv.Itoa(tc.count), func(t *testing.T) {
			pb := ir.NewProgramBuilder()
			entry := pb.Entry()
			types := make([]ir.Type, tc.count)
			for i := range types {
				types[i] = ir.Scalar(32)
			}
			loop, done := pb.NewBlock(types...), pb.NewBlock(ir.Scalar(32), ir.Scalar(32))
			initial := make([]int, tc.count)
			initial[0] = labelConst386(pb, entry, 32, 1)
			for i := 1; i < tc.count-2; i++ {
				initial[i] = labelConst386(pb, entry, 32, uint64(i))
			}
			if tc.cycle {
				initial[tc.count-3] = labelConst386(pb, entry, 32, 3)
			}
			initial[tc.count-2] = labelConst386(pb, entry, 32, 11)
			initial[tc.count-1] = labelConst386(pb, entry, 32, 22)
			pb.Jump(entry, loop, initial...)
			values := labelParams386(pb, loop, tc.count)
			nextCount := pb.Append(loop, ir.Instruction{Op: ir.OpSub, Bits: 32, A: values[0], B: labelConst386(pb, loop, 32, 1)})
			next := append([]int(nil), values...)
			next[0] = nextCount
			if tc.cycle {
				next[tc.count-3], next[tc.count-2], next[tc.count-1] = values[tc.count-1], values[tc.count-3], values[tc.count-2]
			} else {
				next[tc.count-2], next[tc.count-1] = values[tc.count-1], values[tc.count-2]
			}
			pb.Branch(loop, labelIsZero386(values[0]), done, []int{values[tc.count-2], values[tc.count-1]}, loop, next)
			result := labelParams386(pb, done, 2)
			left := pb.Append(done, ir.Instruction{Op: ir.OpMul, Bits: 32, A: result[0], B: labelConst386(pb, done, 32, 100)})
			pb.Return(done, pb.Append(done, ir.Instruction{Op: ir.OpAdd, Bits: 32, A: left, B: result[1]}))
			if got := compileLabel386(t, pb); got != tc.want {
				t.Fatalf("permutation result = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestWideLabelArgument386(t *testing.T) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	done := pb.NewBlock(ir.Scalar(64))
	pb.Jump(entry, done, labelConst386(pb, entry, 64, 0x1122334455667788))
	pb.Return(done, pb.BlockParam(done, 0))
	if got := compileLabel386(t, pb); got != 0x1122334455667788 {
		t.Fatalf("wide label result = %#x, want %#x", got, uint64(0x1122334455667788))
	}
}

func TestWideLabelArgumentsSwap386(t *testing.T) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	loop, done := pb.NewBlock(ir.Scalar(32), ir.Scalar(64), ir.Scalar(64)), pb.NewBlock(ir.Scalar(64))
	pb.Jump(entry, loop,
		labelConst386(pb, entry, 32, 1),
		labelConst386(pb, entry, 64, 0x1122334455667788),
		labelConst386(pb, entry, 64, 0x99aabbccddeeff00))
	values := labelParams386(pb, loop, 3)
	nextCount := pb.Append(loop, ir.Instruction{Op: ir.OpSub, Bits: 32, A: values[0], B: labelConst386(pb, loop, 32, 1)})
	pb.Branch(loop, labelIsZero386(values[0]), done, []int{values[1]}, loop, []int{nextCount, values[2], values[1]})
	pb.Return(done, pb.BlockParam(done, 0))
	if got := compileLabel386(t, pb); got != 0x99aabbccddeeff00 {
		t.Fatalf("swapped wide label result = %#x", got)
	}
}

// A 128-bit value crosses an edge as its two 64-bit halves.
func TestComposite128LabelArgument386(t *testing.T) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	done := pb.NewBlock(ir.Scalar(64), ir.Scalar(64))
	pb.Jump(entry, done,
		labelConst386(pb, entry, 64, 0x1122334455667788),
		labelConst386(pb, entry, 64, 0x99aabbccddeeff00))
	pb.Return(done, pb.BlockParam(done, 0))
	if got := compileLabel386(t, pb); got != 0x1122334455667788 {
		t.Fatalf("128-bit label low word = %#x", got)
	}
}

func TestNarrowLabelArguments386(t *testing.T) {
	for _, width := range []uint8{8, 16} {
		pb := ir.NewProgramBuilder()
		entry := pb.Entry()
		done := pb.NewBlock(ir.Scalar(width))
		pb.Jump(entry, done, labelConst386(pb, entry, width, 7))
		pb.Return(done, pb.BlockParam(done, 0))
		if got := compileLabel386(t, pb); got != 7 {
			t.Fatalf("%d-bit label result = %d, want 7", width, got)
		}
	}
}
