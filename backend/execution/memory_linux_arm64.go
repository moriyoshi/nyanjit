//go:build linux && arm64

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

// flushICache cleans the data cache to PoU and invalidates the instruction
// cache with DSB/ISB on the publishing thread. Publication relies on Linux's
// executable-mapping transition and coherent Inner Shareable caches for other
// threads; it does not provide a process-wide membarrier or support live patching.
func flushICache(start, end uintptr)
func call(address uintptr, args *ArgumentWords, workspace uintptr) uintptr
func callPair(address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr)
func callWithABI(_ abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr) {
	return call(address, args, workspace), 0
}
func callWithABIArity(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, _ int) (uintptr, uintptr) {
	return callWithABI(flavor, address, args, workspace)
}
func callManaged(address uintptr, args *ArgumentWords, workspace uintptr) uintptr
func callPairManaged(address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr)

// callManagedEntry enters Go-managed code without the gap unmanaged code
// needs, so its frame record is the one Go's stack copy adjusts.
func callManagedEntry(pair bool, _ abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, _ int) (uintptr, uintptr) {
	if pair {
		return callPairManaged(address, args, workspace)
	}
	return callManaged(address, args, workspace), 0
}
