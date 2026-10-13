//go:build linux && (arm64 || amd64 || riscv64)

package builder

import (
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"
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

func controlOptions() CompileOptions {
	return CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536}
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

func TestLabelArgumentArityMismatch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		arguments  int
		parameters int
	}{
		{name: "too few", arguments: 0, parameters: 1},
		{name: "too many", arguments: 2, parameters: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder()
			entry, target := b.NewLabel(), b.NewLabel()
			requireControlOK(t, b.Bind(entry))
			values := []Value{controlConst(t, b, 10), controlConst(t, b, 20)}
			requireControlOK(t, b.JumpArgs(target, values[:tc.arguments]...))
			params, err := b.BindParams(target, tc.parameters)
			requireControlOK(t, err)
			requireControlOK(t, b.Return(params[0]))
			if _, err := testAnalyze(b, controlOptions()); !errors.Is(err, ErrInvalidFunction) {
				t.Fatalf("Analyze() = %v, want %v", err, ErrInvalidFunction)
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
	seven, err := b.CompareImmediate(x, BranchEqual, 7)
	requireControlOK(t, err)
	yZero, err := b.IsZero(y)
	requireControlOK(t, err)
	requireControlOK(t, b.Branch(negative.Or(seven).And(yZero.Not()), yes, no))
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
			assembly, asmErr := testEmitAssembly(b, controlOptions())
			t.Fatalf("Call(%#x, %d) = %d, want %d (assembly error %v):\n%s", tc.x, tc.y, got, tc.want, asmErr, assembly)
		}
	}
}

func TestDecrementBranchUsesResult(t *testing.T) {
	b := NewBuilder()
	entry, loop, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	requireControlOK(t, b.Bind(entry))
	count, err := b.Param()
	requireControlOK(t, err)
	requireControlOK(t, b.JumpArgs(loop, count))
	parameters, err := b.BindParams(loop, 1)
	requireControlOK(t, err)
	one := controlConst(t, b, 1)
	next := controlBinary(t, b, opSub, parameters[0], one)
	nonzero, err := b.CompareImmediate(next, BranchNotEqual, 0)
	requireControlOK(t, err)
	requireControlOK(t, b.BranchArgs(nonzero, loop, []Value{next}, done, nil))
	requireControlOK(t, b.Bind(done))
	requireControlOK(t, b.Return(controlConst(t, b, 42)))
	assembly, err := testEmitAssembly(b, controlOptions())
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
	code, err := testCompileWithOptions(b, controlOptions())
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

func buildEdgePermutationLoop(t *testing.T, count int, cycle bool) *Builder {
	t.Helper()
	b := NewBuilder()
	entry, loop, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	requireControlOK(t, b.Bind(entry))
	initial := make([]Value, count)
	initial[0] = controlConst(t, b, 1)
	for i := 1; i < count-2; i++ {
		initial[i] = controlConst(t, b, uint64(i))
	}
	initial[count-2] = controlConst(t, b, 11)
	initial[count-1] = controlConst(t, b, 22)
	requireControlOK(t, b.JumpArgs(loop, initial...))
	values, err := b.BindParams(loop, count)
	requireControlOK(t, err)
	predicate, err := b.IsZero(values[0])
	requireControlOK(t, err)
	one := controlConst(t, b, 1)
	nextCount := controlBinary(t, b, opSub, values[0], one)
	next := append([]Value(nil), values...)
	next[0] = nextCount
	if cycle {
		next[count-3], next[count-2], next[count-1] = values[count-1], values[count-3], values[count-2]
	} else {
		next[count-2], next[count-1] = values[count-1], values[count-2]
	}
	requireControlOK(t, b.BranchArgs(predicate, done, []Value{values[count-2], values[count-1]}, loop, next))
	result, err := b.BindParams(done, 2)
	requireControlOK(t, err)
	hundred := controlConst(t, b, 100)
	left := controlBinary(t, b, opMul, result[0], hundred)
	answer := controlBinary(t, b, opAdd, left, result[1])
	requireControlOK(t, b.Return(answer))
	return b
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
			b := buildEdgePermutationLoop(t, tc.count, tc.cycle)
			code, report, err := testEmit(b, controlOptions())
			requireControlOK(t, err)
			if len(code) != report.CodeBytes {
				t.Fatalf("code length = %d, report = %d", len(code), report.CodeBytes)
			}
			executable, err := testCompileWithOptions(b, controlOptions())
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

func TestConditionalEdgeStagesLargeComparison(t *testing.T) {
	b := NewBuilder()
	entry, yes, no := b.NewLabel(), b.NewLabel(), b.NewLabel()
	requireControlOK(t, b.Bind(entry))
	x, err := b.Param()
	requireControlOK(t, err)
	predicate, err := b.CompareImmediate(x, BranchEqual, 10000)
	requireControlOK(t, err)
	requireControlOK(t, b.BranchArgs(predicate, yes, []Value{x}, no, []Value{x}))
	yesValue, err := b.BindParams(yes, 1)
	requireControlOK(t, err)
	requireControlOK(t, b.Return(yesValue[0]))
	noValue, err := b.BindParams(no, 1)
	requireControlOK(t, err)
	one := controlConst(t, b, 1)
	answer := controlBinary(t, b, opAdd, noValue[0], one)
	requireControlOK(t, b.Return(answer))
	code, err := testCompileWithOptions(b, controlOptions())
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
