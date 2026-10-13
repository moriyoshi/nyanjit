//go:build linux && 386

package execution

import (
	"syscall"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// makeExecutable seals mem read-only and executable after its first n bytes
// were written.
func makeExecutable(mem []byte, _ int) error {
	return syscall.Mprotect(mem, syscall.PROT_READ|syscall.PROT_EXEC)
}

func entry(mem []byte) uintptr { return uintptr(unsafe.Pointer(&mem[0])) }
func call386(address uintptr, args *ArgumentWords, workspace uintptr) (lo, hi uintptr)
func callGo386(address uintptr, args *ArgumentWords, workspace uintptr, words int) (lo, hi uintptr)

// The 386 C ABI already returns two words in EAX:EDX.
func callPair(address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr) {
	return call386(address, args, workspace)
}
func callWithABI(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr) {
	if flavor != abi.C {
		panic(ErrInvalidCodeOptions)
	}
	return call386(address, args, workspace)
}
func callWithABIArity(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, words int) (uintptr, uintptr) {
	if flavor == abi.Go {
		return callGo386(address, args, workspace, words)
	}
	if flavor != abi.C {
		panic(ErrInvalidCodeOptions)
	}
	return call386(address, args, workspace)
}
