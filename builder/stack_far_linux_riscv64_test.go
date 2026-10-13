//go:build linux && riscv64

package builder

import "testing"

func TestFarWorkspaceSpills(t *testing.T) {
	const count = 320
	b := buildSpillPressureFunctionCount(t, count)
	options := CompileOptions{ABI: ABIC, Stack: StackNoUse}
	report, err := testAnalyze(b, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.SpillStorage != SpillWorkspace || report.WorkspaceBytes <= 2048 {
		t.Fatalf("expected far workspace spills, report = %+v", report)
	}
	code, err := testCompileWithOptions(b, options)
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
