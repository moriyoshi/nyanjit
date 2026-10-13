//go:build linux && amd64

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
func call(address uintptr, args *ArgumentWords, workspace uintptr) uintptr
func callPair(address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr)
func callGo(address uintptr, args *ArgumentWords, workspace uintptr) uintptr
func callWithABI(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr) {
	if flavor == abi.Go {
		return callGo(address, args, workspace), 0
	}
	return call(address, args, workspace), 0
}
func callWithABIArity(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, _ int) (uintptr, uintptr) {
	return callWithABI(flavor, address, args, workspace)
}
