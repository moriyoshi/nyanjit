//go:build linux && loong64

package compile_test

import "testing"

// TestBaselineManagedLoong64 runs the loong64 Go-managed call tests at
// TierBaseline.
func TestBaselineManagedLoong64(t *testing.T) {
	atBaseline(t, map[string]func(*testing.T){
		"SixteenGoArgumentsAndHomes":   TestLoong64SixteenGoArgumentsAndHomes,
		"CWordExtensionAndCallbacks":   TestLoong64CWordExtensionAndCallbacks,
		"ForeignThreadReentersManaged": TestLoong64ForeignThreadReentersRegisteredCode,
	})
}
