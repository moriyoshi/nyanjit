//go:build linux && (arm64 || amd64 || riscv64)

package builder

import (
	"errors"
	"runtime"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestPreflightCallBoundaryEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec abi.CallSpec
	}{
		{name: "C function", spec: abi.CallSpec{Kind: abi.FunctionCall, ABI: abi.C, ArgumentCount: 2}},
		{name: "Go function", spec: abi.CallSpec{Kind: abi.FunctionCall, ABI: abi.Go, ArgumentCount: 2}},
		{name: "system call", spec: abi.CallSpec{Kind: abi.SystemCall, ABI: abi.C, ArgumentCount: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			effect, err := compile.PreflightCall(tc.spec)
			if err != nil {
				t.Fatal(err)
			}
			if effect.Architecture != runtime.GOARCH || effect.Kind != tc.spec.Kind || effect.ABI != tc.spec.ABI {
				t.Fatalf("boundary identity = %+v, want %s / %d / %d", effect, runtime.GOARCH, tc.spec.Kind, tc.spec.ABI)
			}
			if len(effect.ArgumentRegisters) != tc.spec.ArgumentCount || effect.ResultRegister == "" ||
				!slices.Contains(effect.ClobberedRegisters, effect.ResultRegister) {
				t.Fatalf("incomplete register effect: %+v", effect)
			}
			if effect.FlagsClobbered != (runtime.GOARCH != "riscv64") || !effect.MayReadMemory || !effect.MayWriteMemory {
				t.Fatalf("boundary omits conservative effects: %+v", effect)
			}
			if tc.spec.Kind == abi.SystemCall {
				if effect.NumberRegister == "" || effect.RequiresCallStack || effect.StackAlignment != 0 {
					t.Fatalf("incorrect system-call stack policy: %+v", effect)
				}
			} else if effect.NumberRegister != "" || !effect.RequiresCallStack || effect.StackAlignment <= 0 {
				t.Fatalf("incorrect function-call stack policy: %+v", effect)
			}
		})
	}
}

func TestPreflightCallArchitectureConventions(t *testing.T) {
	for _, tc := range []struct {
		kind abi.CallKind
		abi  abi.Flavor
		want []string
	}{
		{kind: abi.FunctionCall, abi: abi.C, want: map[string][]string{
			"arm64": {"X0", "X1"}, "amd64": {"RDI", "RSI"}, "riscv64": {"X10", "X11"},
		}[runtime.GOARCH]},
		{kind: abi.FunctionCall, abi: abi.Go, want: map[string][]string{
			"arm64": {"X0", "X1"}, "amd64": {"RAX", "RBX"}, "riscv64": {"X10", "X11"},
		}[runtime.GOARCH]},
		{kind: abi.SystemCall, abi: abi.C, want: map[string][]string{
			"arm64": {"X0", "X1"}, "amd64": {"RDI", "RSI"}, "riscv64": {"X10", "X11"},
		}[runtime.GOARCH]},
	} {
		effect, err := compile.PreflightCall(abi.CallSpec{Kind: tc.kind, ABI: tc.abi, ArgumentCount: 2})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(effect.ArgumentRegisters, tc.want) {
			t.Fatalf("%s call kind %d ABI %d arguments = %v, want %v", runtime.GOARCH, tc.kind, tc.abi, effect.ArgumentRegisters, tc.want)
		}
	}
	syscall, err := compile.PreflightCall(abi.CallSpec{Kind: abi.SystemCall, ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOARCH == "amd64" {
		if syscall.Precision != abi.EffectPlatformGuaranteed || !slices.Equal(syscall.ClobberedRegisters, []string{"RAX", "RCX", "R11"}) {
			t.Fatalf("amd64 syscall clobbers = %+v", syscall)
		}
	} else {
		numberRegister := "X8"
		if runtime.GOARCH == "riscv64" {
			numberRegister = "X17"
		}
		if syscall.Precision != abi.EffectConservative || syscall.NumberRegister != numberRegister {
			t.Fatalf("%s syscall clobbers = %+v", runtime.GOARCH, syscall)
		}
	}
}

func TestPreflightCallRejectsInvalidSpecifications(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec abi.CallSpec
		want error
	}{
		{name: "kind", spec: abi.CallSpec{Kind: abi.CallKind(255)}, want: ErrInvalidFunction},
		{name: "negative count", spec: abi.CallSpec{ArgumentCount: -1}, want: ErrBackendLimit},
		{name: "too many arguments", spec: abi.CallSpec{ArgumentCount: backendMaxArgs + 1}, want: ErrBackendLimit},
		{name: "too many syscall arguments", spec: abi.CallSpec{Kind: abi.SystemCall, ArgumentCount: 7}, want: ErrBackendLimit},
		{name: "unknown function ABI", spec: abi.CallSpec{Kind: abi.FunctionCall, ABI: abi.Flavor(255)}, want: ErrUnsupportedABI},
		{name: "Go syscall ABI", spec: abi.CallSpec{Kind: abi.SystemCall, ABI: abi.Go}, want: ErrUnsupportedABI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := compile.PreflightCall(tc.spec); !errors.Is(err, tc.want) {
				t.Fatalf("compile.PreflightCall(%+v) = %v, want %v", tc.spec, err, tc.want)
			}
		})
	}
}

func TestOutboundCallAnalysisAndExecutionBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		abi  abi.Flavor
		call func(*Builder, Value, ...Value) (Value, error)
	}{
		{name: "C ABI", abi: abi.C, call: (*Builder).CallC},
		{name: "Go ABI", abi: abi.Go, call: (*Builder).CallGo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBuilder()
			argument, err := b.Param()
			if err != nil {
				t.Fatal(err)
			}
			target, err := b.Const(0)
			if err != nil {
				t.Fatal(err)
			}
			result, err := tc.call(b, target, argument)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.Return(result); err != nil {
				t.Fatal(err)
			}
			f := compile.Func{ABI: tc.abi, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
			external := f
			external.Context = compile.ContextExternal
			report, err := testReport(b, compile.TargetSpec{}, external)
			if err != nil {
				t.Fatal(err)
			}
			if !report.HasFunctionCalls() || !report.MayReadMemory() || !report.MayWriteMemory() || report.CodeBytes() == 0 {
				t.Fatalf("incomplete outbound call report: %+v", report.Snapshot())
			}
			if _, err := testCompileWithOptions(b, f); !errors.Is(err, ErrExternalCallOnly) {
				t.Fatalf("CompileWithOptions() = %v, want %v", err, ErrExternalCallOnly)
			}
		})
	}
}

func TestCallCWithEffectCopiesAndUsesSummary(t *testing.T) {
	effect, err := compile.PreflightCall(abi.CallSpec{Kind: abi.FunctionCall, ABI: abi.C, ArgumentCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	effect.MayReadMemory, effect.MayWriteMemory = false, false
	b := NewBuilder()
	target, err := b.Const(0)
	if err != nil {
		t.Fatal(err)
	}
	argument, err := b.Const(7)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.CallCWithEffect(target, effect, argument)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	effect.ArgumentRegisters[0] = "invalid"
	effect.ClobberedRegisters[0] = "invalid"
	report, err := testReport(b, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextExternal, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasFunctionCalls() || report.MayReadMemory() || report.MayWriteMemory() {
		t.Fatalf("callee-specific effect was not preserved: %+v", report.Snapshot())
	}
}
