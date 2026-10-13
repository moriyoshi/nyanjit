//go:build linux && riscv64

package builder

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestFarWorkspaceSpills(t *testing.T) {
	const count = 320
	b := buildSpillPressureFunctionCount(t, count)
	f := compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}
	report, err := testReport(b, compile.TargetSpec{}, f)
	if err != nil {
		t.Fatal(err)
	}
	if report.SpillStorage() != abi.SpillWorkspace || report.WorkspaceBytes() <= 2048 {
		t.Fatalf("expected far workspace spills, report = %+v", report.Snapshot())
	}
	code, err := testCompileWithOptions(b, f)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	for _, input := range []uint64{0, 7, ^uint64(0)} {
		got, err := code.Call(uintptr(input))
		if err != nil {
			t.Fatal(err)
		}
		want := uint64(count)*input + uint64(count*(count+1)/2)
		if uint64(got) != want {
			t.Fatalf("Call(%#x) = %#x, want %#x", input, got, want)
		}
	}
}
