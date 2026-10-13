//go:build linux && 386

package execution

import "github.com/moriyoshi/nyanjit/backend/abi"

func call386Float(block *floatCall, workspace uintptr)
func floatForeignEntry() uintptr

var floatForeignAddress = floatForeignEntry()

// enterFloat enters a cdecl entry whose float result returns in x87 ST(0);
// it is the only 386 entry with a float plan.
func enterFloat(_ *floatPlan, _ bool, _ abi.Flavor, block *floatCall, workspace uintptr) {
	call386Float(block, workspace)
}
