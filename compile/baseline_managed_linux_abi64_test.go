//go:build !nyanjit_no_goregister && linux && (amd64 || arm64 || riscv64 || ppc64le || loong64)

package compile_test

import "testing"

// TestBaselineManagedGoModule runs the Go-module registration tests at
// TierBaseline: Go calls that collect garbage, grow the stack, reenter
// generated code and panic through it.
func TestBaselineManagedGoModule(t *testing.T) {
	atBaseline(t, map[string]func(*testing.T){
		"ScansGeneratedCaller":          TestGoModuleScansGeneratedCaller,
		"RegistersFramelessLeaf":        TestGoModuleRegistersFramelessLeaf,
		"ReentersGeneratedCode":         TestGoModuleReentersGeneratedCode,
		"SurvivesStackGrowth":           TestGoModuleSurvivesStackGrowth,
		"UnwindsPanic":                  TestGoModuleUnwindsPanic,
		"CloseRetainsRuntimeMetadata":   TestGoModuleCloseRetainsRuntimeMetadata,
		"CallAcrossBuilderLabels":       TestGoModuleCallAcrossBuilderLabels,
		"ReservesRegisterArgumentHomes": TestGoCallReservesRegisterArgumentHomes,
	})
}

// TestBaselineManagedMixedABI runs the mixed C and Go call tests of
// Go-managed entries at TierBaseline.
func TestBaselineManagedMixedABI(t *testing.T) {
	atBaseline(t, map[string]func(*testing.T){
		"FullCArity":                   TestManagedCallUsesFullCArity,
		"CEntryCallsGo":                TestCEntryCallsGoInManagedContext,
		"MixedCallChainPreservesValue": TestManagedMixedCallChainPreservesLiveValue,
		"CEntryFromCallbackTrampoline": TestCEntryCalledFromRuntimeCallbackTrampoline,
		"ForeignRejectsGoCall":         TestForeignContextRejectsDirectGoCall,
		"GoEntryCallsCThroughGateway":  TestGoEntryCallsCThroughRuntimeGateway,
		"ZeroArgumentCCall":            TestManagedZeroArgumentCCall,
		"CEntryBranchesToGoAndCCalls":  TestCEntryBranchesToGoAndCCalls,
	})
}
