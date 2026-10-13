//go:build linux && (amd64 || arm64 || riscv64)

package compile

import (
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestManagedCEntryCallPreservesEntryABI(t *testing.T) {
	program := &ir.Program{Instructions: []ir.Instruction{
		{Op: ir.OpParam, Bits: 64},
		{Op: ir.OpConst, Imm: 1},
		{Op: ir.OpCallGo, Args: []int{1, 0}},
	}, Arity: 1, Result: 2}
	stackBytes := 240
	if runtime.GOARCH == "riscv64" {
		stackBytes = 512
	}
	report, err := Analyze(program, CompileOptions{ABI: ABIC, ManagedCalls: true, Stack: StackPlatform, StackBytesAvailable: stackBytes})
	if err != nil {
		t.Fatal(err)
	}
	if report.ABI != ABIC || !report.GoManaged || !report.HasFunctionCalls || report.StackBytes == 0 {
		t.Fatalf("mixed context report = %+v", report)
	}
	var preserved []string
	if runtime.GOARCH == "amd64" {
		preserved = []string{"RBX", "RBP", "R12", "R13", "R14", "R15"}
	} else if runtime.GOARCH == "arm64" {
		preserved = []string{"X19", "X20", "X21", "X22", "X23", "X24", "X25", "X26", "X27", "X28", "X29"}
	} else {
		preserved = []string{"X8", "X9", "X18", "X19", "X20", "X21", "X22", "X23", "X24", "X25", "X26", "X27"}
	}
	for _, name := range preserved {
		for _, clobbered := range report.ClobberedRegisters {
			if clobbered == name {
				t.Fatalf("preserved %s reported clobbered", name)
			}
		}
	}
	if runtime.GOARCH == "arm64" {
		for _, name := range report.ClobberedFPRegisters {
			if name == "V8" || name == "V15" {
				t.Fatalf("saved C ABI vector register %s reported clobbered", name)
			}
		}
	} else if runtime.GOARCH == "riscv64" {
		for _, name := range report.ClobberedFPRegisters {
			if name == "F8" || name == "F9" || name == "F18" || name == "F27" {
				t.Fatalf("saved C ABI floating-point register %s reported clobbered", name)
			}
		}
	}
}

func TestManagedCBridgePreflightUsesGoRegisters(t *testing.T) {
	program := &ir.Program{Instructions: []ir.Instruction{
		{Op: ir.OpConst, Imm: 1},
		{Op: ir.OpConst, Imm: 2},
		{Op: ir.OpCallC, Args: []int{0, 1}},
	}, Result: 2}
	scopes, err := PreflightRegisterScopes(program, CompileOptions{ABI: ABIC, ManagedCalls: true, Stack: StackPlatform})
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) == 0 {
		t.Fatal("no root scope")
	}
	want := []string{"RAX", "RBX"}
	if runtime.GOARCH == "arm64" {
		want = []string{"X0", "X1"}
	} else if runtime.GOARCH == "riscv64" {
		want = []string{"X10", "X11"}
	}
	for id, register := range want {
		found := false
		for _, use := range scopes[0].FixedUses {
			if use.Value == id && use.Register == register {
				found = true
			}
		}
		if !found {
			t.Fatalf("value %d lacks Go argument register %s: %+v", id, register, scopes[0].FixedUses)
		}
	}
	wantFP := "XMM8"
	if runtime.GOARCH == "arm64" {
		wantFP = "V8"
	} else if runtime.GOARCH == "riscv64" {
		wantFP = "F8"
	}
	foundFP := false
	for _, name := range scopes[0].ClobberedFPRegisters {
		if name == wantFP {
			foundFP = true
		}
	}
	if !foundFP {
		t.Fatalf("managed C bridge preflight lacks %s: %+v", wantFP, scopes[0].ClobberedFPRegisters)
	}
}
