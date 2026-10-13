//go:build linux && (amd64 || arm64 || 386 || ppc64le)

package execution

import "github.com/ebitengine/purego"

func callForeign(address uintptr, args []uintptr) (uintptr, uintptr) {
	if len(args) > MaxArgumentWords {
		panic(ErrArity)
	}
	low, high, _ := purego.SyscallN(address, args...)
	return low, high
}
