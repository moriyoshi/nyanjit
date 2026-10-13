//go:build linux && (loong64 || riscv64)

package compile_test

import "testing"

// TestBaselineManagedLP64 runs the LoongArch and RISC-V Go-managed C ABI
// tests at TierBaseline.
func TestBaselineManagedLP64(t *testing.T) {
	atBaseline(t, map[string]func(*testing.T){
		"CArgumentExtension":            TestLP64CArgumentExtension,
		"GoEntryReportsPreservedWrites": TestGoEntryReportsPreservedRegisterWrites,
		"ManagedLeafAvoidsCallSaveSet":  TestLP64ManagedLeafAvoidsCallSaveSet,
	})
}
