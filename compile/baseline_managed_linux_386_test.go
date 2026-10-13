//go:build !nyanjit_no_goregister && linux && 386

package compile_test

import "testing"

// TestBaselineManaged386 runs the 386 Go-managed and cdecl call tests at
// TierBaseline.
func TestBaselineManaged386(t *testing.T) {
	atBaseline(t, map[string]func(*testing.T){
		"EntryCallsGo":           Test386ManagedEntryCallsGo,
		"EntriesCallC":           Test386ManagedEntriesCallC,
		"MaximumGoCallArity":     Test386MaximumGoCallArity,
		"CToGoCallback":          Test386ManagedCToGoCallback,
		"GoEntryBranchResult":    Test386ManagedGoEntryBranchResult,
		"CdeclManagedOddWords":   Test386CdeclManagedOddWords,
		"CdeclMixedEntry":        Test386CdeclMixedEntry,
		"CdeclMixedOutgoing":     Test386CdeclMixedOutgoing,
		"CdeclGoSubwordsRejects": Test386CdeclGoSubwordsRejected,
	})
}
