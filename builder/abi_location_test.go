package builder

import (
	"errors"
	"github.com/moriyoshi/nyanjit/compile"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
)

func TestEmissionReportLocations(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("native target required")
	}
	b := NewBuilder()
	arg, err := b.Param()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(arg); err != nil {
		t.Fatal(err)
	}
	report, err := testAnalyze(b, CompileOptions{ABI: ABIC})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.ArgumentLocations) != 1 || abi.SingleRegister(report.ArgumentLocations[0]) != report.ArgumentRegisters[0] ||
		abi.SingleRegister(report.ResultLocation) != report.ResultRegister {
		t.Fatalf("legacy and ordered locations disagree: %+v", report)
	}
	if runtime.GOARCH == "amd64" {
		b = NewBuilder()
		for i := 0; i < 6; i++ {
			arg, err = b.Param()
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.Param(); !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("seventh amd64 parameter = %v, want ErrBackendLimit", err)
		}
		if _, err := compile.PreflightCall(CallSpec{Kind: FunctionCall, ABI: ABIC, ArgumentCount: 7}); !errors.Is(err, ErrBackendLimit) {
			t.Fatalf("seventh amd64 call argument = %v, want ErrBackendLimit", err)
		}
	}
}
