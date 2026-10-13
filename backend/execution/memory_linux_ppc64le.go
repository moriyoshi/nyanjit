//go:build linux && ppc64le

package execution

import (
	"syscall"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// makeExecutable seals mem read-only and executable after its first n bytes
// were written.
func makeExecutable(mem []byte, n int) error {
	start := uintptr(unsafe.Pointer(&mem[0]))
	flushICache(start, start+uintptr(n))
	return syscall.Mprotect(mem, syscall.PROT_READ|syscall.PROT_EXEC)
}

func entry(mem []byte) uintptr { return uintptr(unsafe.Pointer(&mem[0])) }
func flushICache(start, end uintptr)
func call(address uintptr, args *ArgumentWords, workspace uintptr) uintptr
func callPair(address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr)

// The first eight uintptr registers coincide for C and ABIInternal.
func callWithABI(_ abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr) {
	return call(address, args, workspace), 0
}
func callWithABIArity(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, _ int) (uintptr, uintptr) {
	return callWithABI(flavor, address, args, workspace)
}
