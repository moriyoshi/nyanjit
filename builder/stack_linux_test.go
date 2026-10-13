//go:build linux && (arm64 || amd64 || riscv64)

package builder

import (
	"errors"
	"math"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func buildSpillPressureFunction(t *testing.T) *Builder {
	return buildSpillPressureFunctionCount(t, 40)
}

func buildSpillPressureFunctionCount(t *testing.T, count int) *Builder {
	t.Helper()
	b := NewBuilder()
	x, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	values := make([]Value, count)
	for i := range values {
		constant, err := b.Const(uint64(i + 1))
		if err != nil {
			t.Fatal(err)
		}
		values[i], err = b.Add(x, constant)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Leave(values...); err != nil {
		t.Fatal(err)
	}
	result := values[0]
	for _, value := range values[1:] {
		result, err = b.Add(result, value)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSpillStorageFollowsStackPractice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		f       compile.Func
		storage abi.SpillStorage
	}{
		{name: "caller workspace", f: compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}, storage: abi.SpillWorkspace},
		{name: "platform stack", f: compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}, storage: abi.SpillStack},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := buildSpillPressureFunction(t)
			report, err := testReport(b, compile.TargetSpec{}, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			if report.SpillStorage() != tc.storage {
				t.Fatalf("spill storage = %d, want %d; report = %+v", report.SpillStorage(), tc.storage, report.Snapshot())
			}
			if tc.storage == abi.SpillWorkspace && (report.WorkspaceBytes() == 0 || report.StackBytes() != 0) {
				t.Fatalf("caller workspace report = %+v", report.Snapshot())
			}
			if tc.storage == abi.SpillStack && (report.StackBytes() == 0 || report.WorkspaceBytes() != 0) {
				t.Fatalf("platform stack report = %+v", report.Snapshot())
			}
			code, err := testCompileWithOptions(b, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			defer code.Close()
			for _, x := range []uint64{0, 7, math.MaxUint64} {
				got, err := code.Call(uintptr(x))
				if err != nil {
					t.Fatal(err)
				}
				if want := 40*x + 820; uint64(got) != want {
					t.Fatalf("Call(%#x) = %#x, want %#x", x, got, want)
				}
			}
		})
	}
}

func TestSpillPressureRejectsInsufficientPlatformStack(t *testing.T) {
	b := buildSpillPressureFunction(t)
	_, err := testReport(b, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform}})
	if !errors.Is(err, ErrStackRequired) {
		t.Fatalf("Analyze() with no stack capacity = %v, want %v", err, ErrStackRequired)
	}
}
