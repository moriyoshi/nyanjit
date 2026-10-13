//go:build !linux || !ppc64

package execution

import (
	"github.com/moriyoshi/nyanjit/abi"
	"runtime"
)

func callForeignPPC64(uintptr, []uintptr, abi.PPC64CABI) uintptr { panic(ErrUnsupported) }

func registerPPC64ForeignCallback(fn any, convention abi.PPC64CABI) (uintptr, error) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "ppc64le" && convention == abi.PPC64ELFv2 {
		return registerForeignCallback(fn)
	}
	return 0, ErrInvalidCodeOptions
}
