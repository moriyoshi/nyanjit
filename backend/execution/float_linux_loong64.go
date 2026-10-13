//go:build linux && loong64

package execution

import "github.com/moriyoshi/nyanjit/backend/abi"

func callFloat(block *floatCall, workspace uintptr)
func floatForeignEntry() uintptr

var floatForeignAddress = floatForeignEntry()

// enterFloat uses one trampoline: the first eight integer and FP argument
// registers coincide for C and Go ABIInternal.
func enterFloat(_ *floatPlan, _ bool, _ abi.Flavor, block *floatCall, workspace uintptr) {
	callFloat(block, workspace)
}
