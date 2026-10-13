//go:build linux && ppc64le

package compile_test

import "testing"

// TestBaselineManagedPPC64 runs the ppc64le Go-managed call tests at
// TierBaseline.
func TestBaselineManagedPPC64(t *testing.T) {
	atBaseline(t, map[string]func(*testing.T){
		"GoCallArityAndCEntryPreservation": TestPPC64GoCallArityAndCEntryPreservation,
		"ManagedCLeafNeedsNoFrame":         TestPPC64ManagedCLeafNeedsNoPreservationFrame,
		"BlockingCCallAllowsGC":            TestPPC64BlockingCCallAllowsGC,
	})
}
