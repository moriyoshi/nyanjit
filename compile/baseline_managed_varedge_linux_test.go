//go:build linux && (amd64 || arm64 || riscv64 || ppc64le)

package compile_test

import "testing"

// TestBaselineManagedVarEdge runs the Var-edge call test, whose callers
// include Go-managed entries, at TierBaseline. On 386 the baseline callee's
// frame exceeds the 240-byte call limit.
func TestBaselineManagedVarEdge(t *testing.T) {
	atBaseline(t, map[string]func(*testing.T){"AcrossForeignCall": TestVarEdgeAcrossForeignCall})
}
