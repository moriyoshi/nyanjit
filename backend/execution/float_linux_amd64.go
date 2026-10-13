//go:build linux && amd64

package execution

import "github.com/moriyoshi/nyanjit/backend/abi"

func callFloat(block *floatCall, workspace uintptr)
func callGoFloat(block *floatCall, workspace uintptr)
func floatForeignEntry() uintptr

var floatForeignAddress = floatForeignEntry()

// enterFloat chooses the integer register order of the entry's convention;
// both load XMM0–XMM7.
func enterFloat(_ *floatPlan, _ bool, flavor abi.Flavor, block *floatCall, workspace uintptr) {
	if flavor == abi.Go {
		callGoFloat(block, workspace)
		return
	}
	callFloat(block, workspace)
}
