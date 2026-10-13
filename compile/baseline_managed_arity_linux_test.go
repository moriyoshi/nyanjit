//go:build !nyanjit_no_goregister && linux && (amd64 || arm64 || riscv64)

package compile_test

import "testing"

// TestBaselineManagedGoCallArity runs the Go call arity tests at
// TierBaseline.
func TestBaselineManagedGoCallArity(t *testing.T) {
	atBaseline(t, map[string]func(*testing.T){
		"Maximum":              TestMaximumGoCallArity,
		"MaximumRegisterMoves": TestMaximumGoCallArityWithRegisterMoves,
	})
}
