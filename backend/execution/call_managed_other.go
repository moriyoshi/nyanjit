//go:build !linux || !arm64

package execution

import "github.com/moriyoshi/nyanjit/backend/abi"

// callManagedEntry enters Go-managed code through the ordinary trampolines,
// which already follow Go's frame convention on these targets.
func callManagedEntry(pair bool, flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, words int) (uintptr, uintptr) {
	if pair {
		return callPair(address, args, workspace)
	}
	return callWithABIArity(flavor, address, args, workspace, words)
}
