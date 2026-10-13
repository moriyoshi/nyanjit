//go:build linux && (arm64 || amd64 || riscv64)

package builder

import (
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func requireControlValue(t *testing.T, value Value, err error) Value {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func controlConst(t *testing.T, b *Builder, value uint64) Value {
	t.Helper()
	result, err := b.Const(value)
	return requireControlValue(t, result, err)
}

func controlBinary(t *testing.T, b *Builder, operation op, left, right Value) Value {
	t.Helper()
	var result Value
	var err error
	switch operation {
	case opAdd:
		result, err = b.Add(left, right)
	case opSub:
		result, err = b.Sub(left, right)
	case opMul:
		result, err = b.Mul(left, right)
	default:
		t.Fatalf("unsupported test operation %d", operation)
	}
	return requireControlValue(t, result, err)
}

func requireControlOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func controlOptions() compile.Func {
	return compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
}

func TestBranchRespectsScopeBoundaries(t *testing.T) {
	b := NewBuilder()
	entry, outer := b.NewLabel(), b.NewLabel()
	requireControlOK(t, b.Bind(entry))
	requireControlOK(t, b.Enter())
	inner := b.NewLabel()
	if err := b.Branch(Predicate{}, outer); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Branch out of scope: got %v, want %v", err, ErrInvalidFunction)
	}
	if err := b.Jump(outer); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Jump out of scope: got %v, want %v", err, ErrInvalidFunction)
	}
	if err := b.Bind(outer); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("Bind outer label in child: got %v, want %v", err, ErrInvalidFunction)
	}
	one := controlConst(t, b, 1)
	predicate, err := b.IsZero(one)
	requireControlOK(t, err)
	requireControlOK(t, b.Branch(predicate, inner))
	requireControlOK(t, b.Branch(Predicate{}, inner))
	requireControlOK(t, b.Bind(inner))
	requireControlOK(t, b.Leave())
	requireControlOK(t, b.Branch(Predicate{}, outer))
	requireControlOK(t, b.Bind(outer))
	answer := controlConst(t, b, 42)
	requireControlOK(t, b.Return(answer))
	code, err := testCompileWithOptions(b, controlOptions())
	requireControlOK(t, err)
	defer code.Close()
	got, err := code.Call()
	requireControlOK(t, err)
	if got != 42 {
		t.Fatalf("scope exit result = %d, want 42", got)
	}
}

func TestNestedScopeExports(t *testing.T) {
	b := NewBuilder()
	base := controlConst(t, b, 10)
	requireControlOK(t, b.Enter())
	outerHidden := controlConst(t, b, 1)
	requireControlOK(t, b.Enter())
	innerHidden := controlConst(t, b, 2)
	innerExport := controlBinary(t, b, opAdd, base, innerHidden)
	requireControlOK(t, b.Leave(innerExport))
	if _, err := b.Add(innerHidden, base); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("using a hidden inner value: got %v, want %v", err, ErrInvalidFunction)
	}
	outerExport := controlBinary(t, b, opAdd, innerExport, outerHidden)
	requireControlOK(t, b.Leave(outerExport))
	for _, hidden := range []Value{innerHidden, innerExport, outerHidden} {
		if _, err := b.Add(hidden, base); !errors.Is(err, ErrInvalidFunction) {
			t.Fatalf("using value %d after outer Leave: got %v, want %v", hidden.id, err, ErrInvalidFunction)
		}
	}
	requireControlOK(t, b.Return(outerExport))
	code, err := testCompileWithOptions(b, controlOptions())
	requireControlOK(t, err)
	defer code.Close()
	got, err := code.Call()
	requireControlOK(t, err)
	if got != 13 {
		t.Fatalf("nested export result = %d, want 13", got)
	}
}

// Builder programs have no label parameters; raw IR does. Lowering rejects
// a raw program whose edge passes the wrong number of arguments.
func TestLabelArgumentArityMismatch(t *testing.T) {
	for _, tc := range []struct {
		name      string
		arguments int
	}{
		{name: "too few", arguments: 0},
		{name: "too many", arguments: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pb := ir.NewProgramBuilder()
			entry := pb.Entry()
			target := pb.NewBlock(ir.Scalar(64))
			ten := pb.Append(entry, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 10})
			pb.Jump(entry, target, ten)
			pb.Return(target, pb.BlockParam(target, 0))
			sealed, err := pb.Seal()
			requireControlOK(t, err)
			program := sealed.Clone()
			edges := 0
			for i := range program.Control.Steps {
				step := &program.Control.Steps[i]
				if !step.IsLabel && step.Term == ir.TermJump && len(step.Args) == 1 {
					step.Args = append(step.Args, step.Args...)[:tc.arguments]
					edges++
				}
			}
			if edges != 1 {
				t.Fatalf("found %d parameterized edges, want 1", edges)
			}
			if _, err := lowerReport(program, compile.TargetSpec{}, controlOptions()); !errors.Is(err, ErrInvalidFunction) {
				t.Fatalf("Lower() = %v, want %v", err, ErrInvalidFunction)
			}
		})
	}
}
func TestComposedPredicates(t *testing.T) {
	b := NewBuilder()
	entry, yes, no := b.NewLabel(), b.NewLabel(), b.NewLabel()
	requireControlOK(t, b.Bind(entry))
	x, err := b.Param()
	requireControlOK(t, err)
	y, err := b.Param()
	requireControlOK(t, err)
	negative, err := b.IsNegative(x)
	requireControlOK(t, err)
	seven, err := b.CompareImmediate(x, ir.BranchEqual, 7)
	requireControlOK(t, err)
	yZero, err := b.IsZero(y)
	requireControlOK(t, err)
	requireControlOK(t, b.Branch(negative.Or(seven).And(yZero.Not()), yes))
	requireControlOK(t, b.Jump(no))
	requireControlOK(t, b.Bind(yes))
	requireControlOK(t, b.Return(controlConst(t, b, 1)))
	requireControlOK(t, b.Bind(no))
	requireControlOK(t, b.Return(controlConst(t, b, 0)))
	code, err := testCompileWithOptions(b, controlOptions())
	requireControlOK(t, err)
	defer code.Close()
	for _, tc := range []struct {
		x, y, want uint64
	}{
		{math.MaxUint64, 1, 1},
		{math.MaxUint64, 0, 0},
		{7, 1, 1},
		{7, 0, 0},
		{6, 1, 0},
		{0, 1, 0},
	} {
		got, err := code.Call(uintptr(tc.x), uintptr(tc.y))
		requireControlOK(t, err)
		if uint64(got) != tc.want {
			assembly, asmErr := testListing(b, compile.TargetSpec{}, controlOptions())
			t.Fatalf("Call(%#x, %d) = %d, want %d (assembly error %v):\n%s", tc.x, tc.y, got, tc.want, asmErr, assembly)
		}
	}
}

// The loop counter is a block parameter of raw IR, so the decrement
// result feeds the branch with no Var home in between.
func TestDecrementBranchUsesResult(t *testing.T) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	count := pb.Param(entry, ir.Scalar(64))
	loop, done := pb.NewBlock(ir.Scalar(64)), pb.NewBlock()
	pb.Jump(entry, loop, count)
	one := pb.Append(loop, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 1})
	next := pb.Append(loop, ir.Instruction{Op: ir.OpSub, Bits: 64, A: pb.BlockParam(loop, 0), B: one})
	nonzero := &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchNotEqual, Value: next}
	pb.Branch(loop, nonzero, loop, []int{next}, done, nil)
	pb.Return(done, pb.Append(done, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 42}))
	program, err := pb.Seal()
	requireControlOK(t, err)
	assembly, err := lowerListing(program, compile.TargetSpec{}, controlOptions())
	requireControlOK(t, err)
	if strings.TrimSpace(assembly) == "" {
		t.Fatal("EmitAssembly returned no instructions")
	}
	decrement, branch := "dec ", "jnz "
	if runtime.GOARCH == "arm64" {
		decrement, branch = "subs ", "b.ne "
	} else if runtime.GOARCH == "riscv64" {
		decrement, branch = "addi ", "bne "
	}
	if !strings.Contains(assembly, decrement) || !strings.Contains(assembly, branch) ||
		strings.Contains(assembly, "cmp ") {
		t.Fatalf("loop does not branch on the decrement result:\n%s", assembly)
	}
	code, err := compileCode(program, compile.TargetSpec{}, controlOptions())
	requireControlOK(t, err)
	defer code.Close()
	for _, count := range []uint64{1, 2, 8} {
		got, err := code.Call(uintptr(count))
		requireControlOK(t, err)
		if got != 42 {
			t.Fatalf("Call(%d) = %d, want 42", count, got)
		}
	}
}

// buildEdgePermutationLoop builds, in raw IR, a loop whose back edge permutes
// its block parameters, so lowering needs parallel edge copies.
func buildEdgePermutationLoop(t *testing.T, count int, cycle bool) *ir.SealedProgram {
	t.Helper()
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	constant := func(block ir.Block, value uint64) int {
		return pb.Append(block, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: value})
	}
	initial := make([]int, count)
	initial[0] = constant(entry, 1)
	for i := 1; i < count-2; i++ {
		initial[i] = constant(entry, uint64(i))
	}
	initial[count-2] = constant(entry, 11)
	initial[count-1] = constant(entry, 22)
	types := make([]ir.Type, count)
	for i := range types {
		types[i] = ir.Scalar(64)
	}
	loop, done := pb.NewBlock(types...), pb.NewBlock(ir.Scalar(64), ir.Scalar(64))
	pb.Jump(entry, loop, initial...)
	values := make([]int, count)
	for i := range values {
		values[i] = pb.BlockParam(loop, i)
	}
	predicate := &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: values[0]}
	nextCount := pb.Append(loop, ir.Instruction{Op: ir.OpSub, Bits: 64, A: values[0], B: constant(loop, 1)})
	next := append([]int(nil), values...)
	next[0] = nextCount
	if cycle {
		next[count-3], next[count-2], next[count-1] = values[count-1], values[count-3], values[count-2]
	} else {
		next[count-2], next[count-1] = values[count-1], values[count-2]
	}
	pb.Branch(loop, predicate, done, []int{values[count-2], values[count-1]}, loop, next)
	left := pb.Append(done, ir.Instruction{Op: ir.OpMul, Bits: 64, A: pb.BlockParam(done, 0), B: constant(done, 100)})
	pb.Return(done, pb.Append(done, ir.Instruction{Op: ir.OpAdd, Bits: 64, A: left, B: pb.BlockParam(done, 1)}))
	program, err := pb.Seal()
	requireControlOK(t, err)
	return program
}
func TestLabelEdgeParallelCopies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		cycle bool
		want  uint64
	}{
		{name: "register swap", count: 3, want: 2211},
		{name: "register cycle", count: 4, cycle: true, want: 111},
		{name: "overflow slot swap", count: 24, want: 2211},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := buildEdgePermutationLoop(t, tc.count, tc.cycle)
			external := controlOptions()
			external.Context = compile.ContextExternal
			template, err := lowerTemplate(program, compile.TargetSpec{}, external)
			requireControlOK(t, err)
			code, err := template.Bytes(nil)
			requireControlOK(t, err)
			if report := template.Report(); len(code) != report.CodeBytes() {
				t.Fatalf("code length = %d, report = %d", len(code), report.CodeBytes())
			}
			executable, err := compileCode(program, compile.TargetSpec{}, controlOptions())
			requireControlOK(t, err)
			defer executable.Close()
			got, err := executable.Call()
			requireControlOK(t, err)
			if uint64(got) != tc.want {
				t.Fatalf("permutation loop result = %d, want %d", got, tc.want)
			}
		})
	}
}

// A comparison immediate too large to encode is staged before a branch whose
// edges both pass arguments (raw IR block parameters).
func TestConditionalEdgeStagesLargeComparison(t *testing.T) {
	pb := ir.NewProgramBuilder()
	entry := pb.Entry()
	x := pb.Param(entry, ir.Scalar(64))
	yes, no := pb.NewBlock(ir.Scalar(64)), pb.NewBlock(ir.Scalar(64))
	predicate := &ir.PredicateExpr{Kind: ir.PredicateCompare, Condition: ir.BranchEqual, Value: x, Immediate: 10000}
	pb.Branch(entry, predicate, yes, []int{x}, no, []int{x})
	pb.Return(yes, pb.BlockParam(yes, 0))
	one := pb.Append(no, ir.Instruction{Op: ir.OpConst, Bits: 64, Imm: 1})
	pb.Return(no, pb.Append(no, ir.Instruction{Op: ir.OpAdd, Bits: 64, A: pb.BlockParam(no, 0), B: one}))
	program, err := pb.Seal()
	requireControlOK(t, err)
	code, err := compileCode(program, compile.TargetSpec{}, controlOptions())
	requireControlOK(t, err)
	defer code.Close()
	for _, tc := range []struct{ input, want uint64 }{{10000, 10000}, {5, 6}} {
		got, err := code.Call(uintptr(tc.input))
		requireControlOK(t, err)
		if uint64(got) != tc.want {
			t.Fatalf("Call(%d) = %d, want %d", tc.input, got, tc.want)
		}
	}
}
