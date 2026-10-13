//go:build linux && (amd64 || arm64 || riscv64)

package builder

import (
	"errors"
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
)

func TestWideReturnCallPair(t *testing.T) {
	b := NewBuilder()
	x, err := b.ParamBits(128)
	if err != nil {
		t.Fatal(err)
	}
	one, err := b.ConstBits(128, 1)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := b.Add(x, one)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(sum); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(program.ResultParts) != 2 || program.ResultParts[0] != program.Result {
		t.Fatalf("result parts = %v, result = %d", program.ResultParts, program.Result)
	}
	options := CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536}
	report, err := compile.Analyze(program, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.ResultWords != 2 || len(report.ResultLocation.Words) != 2 {
		t.Fatalf("result report = %+v", report.ResultLocation)
	}
	code, err := compile.Compile(program, options)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	if _, err := code.Call(math.MaxUint64, 7); !errors.Is(err, execution.ErrResultWidth) {
		t.Fatalf("scalar Call error = %v", err)
	}
	low, high, err := code.CallPair(math.MaxUint64, 7)
	if err != nil || low != 0 || high != 8 {
		t.Fatalf("CallPair = (%#x, %#x, %v), want (0, 8, nil)", low, high, err)
	}
	if _, _, err := code.CallPair(1); !errors.Is(err, execution.ErrArity) {
		t.Fatalf("CallPair arity error = %v", err)
	}
}

// CallPair reads any result portably; a 64-bit host returns a scalar with a
// zero high word.
func TestScalarCallPairReturnsZeroHigh(t *testing.T) {
	code, err := testNewReturn(42)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	if lo, hi, err := code.CallPair(); err != nil || lo != 42 || hi != 0 {
		t.Fatalf("CallPair scalar = %d, %d, %v", lo, hi, err)
	}
}

func TestWideReturnWordOrderWithoutStack(t *testing.T) {
	b := NewBuilder()
	value, err := b.ParamBits(128)
	if err != nil {
		t.Fatal(err)
	}
	low, high := testWideParts(t, b, value)
	if err := b.Return(compositeValue(high, low)); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	clone := program.Clone()
	clone.ResultParts[1] = clone.ResultParts[0]
	if program.ResultParts[0] == program.ResultParts[1] {
		t.Fatal("Clone shared result parts with source")
	}
	if err := clone.Validate(); err == nil {
		t.Fatal("Validate accepted duplicate result parts")
	}
	code, err := compile.Compile(program, CompileOptions{ABI: ABIC, Stack: StackNoUse})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	gotLow, gotHigh, err := code.CallPair(7, 5)
	if err != nil || gotLow != 5 || gotHigh != 7 {
		t.Fatalf("CallPair = (%d, %d, %v), want (5, 7, nil)", gotLow, gotHigh, err)
	}
}

func TestWideReturnAcrossLabels(t *testing.T) {
	b := NewBuilder()
	entry, unchanged, incremented := b.NewLabel(), b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	flag, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.ParamBits(128)
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := b.CompareImmediate(flag, BranchEqual, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BranchArgs(predicate, unchanged, []Value{value}, incremented, []Value{value}); err != nil {
		t.Fatal(err)
	}
	unchangedArgs, err := b.BindParamsBits(unchanged, []uint8{128})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(unchangedArgs[0]); err != nil {
		t.Fatal(err)
	}
	incrementedArgs, err := b.BindParamsBits(incremented, []uint8{128})
	if err != nil {
		t.Fatal(err)
	}
	one, err := b.ConstBits(128, 1)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := b.Add(incrementedArgs[0], one)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(sum); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	clone := program.Clone()
	for i := range clone.Control.Steps {
		if len(clone.Control.Steps[i].ResultParts) == 2 {
			clone.Control.Steps[i].ResultParts[1] = clone.Control.Steps[i].ResultParts[0]
			break
		}
	}
	if err := clone.Validate(); err == nil || program.Validate() != nil {
		t.Fatalf("control clone validation: clone = %v, source = %v", err, program.Validate())
	}
	options := CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536}
	template, err := compile.Prepare(program, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compile.PrepareForeign(program, options); !errors.Is(err, ErrBackendLimit) {
		t.Fatalf("PrepareForeign wide return error = %v", err)
	}
	if report := template.Report(); report.ResultWords != 2 || len(report.ResultLocation.Words) != 2 {
		t.Fatalf("result report = %+v", report)
	}
	code, err := template.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, tc := range []struct {
		flag, low, high   uint64
		wantLow, wantHigh uint64
	}{
		{0, 7, 5, 7, 5},
		{1, math.MaxUint64, 5, 0, 6},
	} {
		gotLow, gotHigh, err := code.CallPair(uintptr(tc.flag), uintptr(tc.low), uintptr(tc.high))
		if err != nil || uint64(gotLow) != tc.wantLow || uint64(gotHigh) != tc.wantHigh {
			t.Fatalf("CallPair(%d, %#x, %#x) = (%#x, %#x, %v), want (%#x, %#x)",
				tc.flag, tc.low, tc.high, gotLow, gotHigh, err, tc.wantLow, tc.wantHigh)
		}
	}
}

func TestWideReturnRegisterSwapAfterLabel(t *testing.T) {
	b := NewBuilder()
	entry := b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	value, err := b.ParamBits(128)
	if err != nil {
		t.Fatal(err)
	}
	low, high := testWideParts(t, b, value)
	if err := b.Return(compositeValue(high, low)); err != nil {
		t.Fatal(err)
	}
	code, err := testCompileWithOptions(b, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	gotLow, gotHigh, err := code.CallPair(9, 11)
	if err != nil || gotLow != 11 || gotHigh != 9 {
		t.Fatalf("CallPair = (%d, %d, %v), want (11, 9, nil)", gotLow, gotHigh, err)
	}
}

func TestMixedReturnWidthsRejected(t *testing.T) {
	b := NewBuilder()
	entry, wideLabel, scalarLabel := b.NewLabel(), b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	flag, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := b.CompareImmediate(flag, BranchEqual, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Branch(predicate, wideLabel, scalarLabel); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(wideLabel); err != nil {
		t.Fatal(err)
	}
	wide, err := b.ConstBits(128, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(wide); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind(scalarLabel); err != nil {
		t.Fatal(err)
	}
	scalar, err := b.Const(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(scalar); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compile.Analyze(program, CompileOptions{ABI: ABIC, Stack: StackPlatform, StackBytesAvailable: 65536}); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("mixed return width error = %v", err)
	}
}
