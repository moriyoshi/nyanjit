package abi

import (
	"reflect"
	"testing"
)

// Every report field has a getter of the same name that returns it, and
// getters never hand out storage shared with the view.
func TestReportGettersCoverEveryField(t *testing.T) {
	report := EmissionReport{
		CPU: CPUProfile{Level: "v", Extensions: []string{"e"}}, VectorOperations: []VectorOperation{{Position: 1}},
		AssumedVectorBytes: 2, Architecture: "a", ABI: Go, PPC64CPU: 1, GoManaged: true, Stack: StackPlatform,
		StackPointer: StackPointerOrdinary, ArgumentRegisters: []string{"r"}, ResultRegister: "r0",
		ArgumentLocations: []ValueLocation{{Words: []WordLocation{{Register: "r"}}}}, ResultLocation: ValueLocation{Words: []WordLocation{{Register: "r0"}}},
		ResultWords: 2, WorkspaceRegister: "w", ClobberedRegisters: []string{"c"}, ClobberedFPRegisters: []string{"f"},
		ClobberedSpecialRegisters: []string{"s"}, FlagsClobbered: true, FPStatusModified: true, ExplicitMemoryReads: true,
		ExplicitMemoryWrites: true, MayReadMemory: true, MayWriteMemory: true, HasSystemCalls: true, HasFunctionCalls: true,
		CodeBytes: 3, WorkspaceBytes: 4, WorkspaceAlignment: 8, StackBytes: 16, CallStackBytes: 32, SpillStorage: SpillStack,
		LoweringStats: LoweringStats{Blocks: 1},
	}
	view := NewReport(report)
	value, fields := reflect.ValueOf(view), reflect.TypeFor[EmissionReport]()
	for i := range fields.NumField() {
		field := fields.Field(i)
		method := value.MethodByName(field.Name)
		if !method.IsValid() {
			t.Errorf("Report has no getter for %s", field.Name)
			continue
		}
		got := method.Call(nil)[0].Interface()
		if want := reflect.ValueOf(report).Field(i).Interface(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s() = %v, want %v", field.Name, got, want)
		}
	}
	if reflect.TypeFor[Report]().NumMethod() != fields.NumField()+1 {
		t.Errorf("Report has %d methods, want one per field plus Snapshot", reflect.TypeFor[Report]().NumMethod())
	}
	view.ArgumentRegisters()[0] = "changed"
	view.ArgumentLocations()[0].Words[0].Register = "changed"
	view.ResultLocation().Words[0].Register = "changed"
	view.CPU().Extensions[0] = "changed"
	view.VectorOperations()[0].Position = 9
	view.Snapshot().ClobberedRegisters[0] = "changed"
	report.ClobberedFPRegisters[0] = "changed"
	if got := view.Snapshot(); got.ArgumentRegisters[0] != "r" || got.ArgumentLocations[0].Words[0].Register != "r" ||
		got.ResultLocation.Words[0].Register != "r0" || got.CPU.Extensions[0] != "e" || got.VectorOperations[0].Position != 1 ||
		got.ClobberedRegisters[0] != "c" || got.ClobberedFPRegisters[0] != "f" {
		t.Fatalf("report view shares storage: %+v", got)
	}
}
