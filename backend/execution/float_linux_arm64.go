//go:build linux && arm64

package execution

import "github.com/moriyoshi/nyanjit/backend/abi"

func callFloat(block *floatCall, workspace uintptr)
func callManagedFloat(block *floatCall, workspace uintptr)
func floatForeignEntry() uintptr

var floatForeignAddress = floatForeignEntry()

// enterFloat enters Go-managed code without the gap unmanaged code needs, as
// callManagedEntry does. C and Go ABIInternal share X0–X7 and V0–V7.
func enterFloat(_ *floatPlan, managed bool, _ abi.Flavor, block *floatCall, workspace uintptr) {
	if managed {
		callManagedFloat(block, workspace)
		return
	}
	callFloat(block, workspace)
}
