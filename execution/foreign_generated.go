package execution

import (
	"github.com/moriyoshi/nyanjit/abi"
	"runtime"
	"unsafe"
)

// callForeignGenerated adapts a raw generated entry, unlike CallForeign,
// which accepts a platform C function pointer (an ELFv1 descriptor on PPC64).
func callForeignGenerated(address uintptr, args []uintptr, convention abi.PPC64CABI) (uintptr, uintptr) {
	if runtime.GOARCH != "ppc64" {
		return CallForeignPair(address, args)
	}
	if convention == abi.PPC64CABIUnspecified {
		convention, _ = abi.DefaultPPC64CABI("ppc64")
	}
	var descriptor *[3]uintptr
	if convention == abi.PPC64ELFv1 {
		descriptor = &[3]uintptr{address, 0, 0}
		address = uintptr(unsafe.Pointer(descriptor))
	}
	result := callForeignPPC64(address, args, convention)
	runtime.KeepAlive(descriptor)
	return result, 0
}
