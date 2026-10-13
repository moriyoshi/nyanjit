//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le)

package execution

import (
	"github.com/ebitengine/purego"
)

func foreignSupported() bool { return true }

func callForeign(address uintptr, args []uintptr) (uintptr, uintptr) {
	if len(args) > MaxArgumentWords {
		panic(ErrArity)
	}
	low, high, _ := purego.SyscallN(address, args...)
	return low, high
}

func registerForeignCallback(fn any) (address uintptr, err error) {
	defer func() {
		if recover() != nil {
			address, err = 0, ErrInvalidCallback
		}
	}()
	return purego.NewCallback(fn), nil
}
