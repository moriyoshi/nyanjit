//go:build linux && (ppc64 || ppc64le)

package execution

import (
	"syscall"
	"unsafe"

	"github.com/moriyoshi/nyanjit/abi"
)

func mapCode(code []byte) ([]byte, error) {
	mem, err := syscall.Mmap(-1, 0, len(code), syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, err
	}
	copy(mem, code)
	start := uintptr(unsafe.Pointer(&mem[0]))
	flushICache(start, start+uintptr(len(code)))
	if err := syscall.Mprotect(mem, syscall.PROT_READ|syscall.PROT_EXEC); err != nil {
		_ = syscall.Munmap(mem)
		return nil, err
	}
	return mem, nil
}

func unmapCode(mem []byte) error { return syscall.Munmap(mem) }
func entry(mem []byte) uintptr   { return uintptr(unsafe.Pointer(&mem[0])) }
func flushICache(start, end uintptr)
func call(address uintptr, args *ArgumentWords, workspace uintptr) uintptr
func callPair(address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr)

// The first eight uintptr registers coincide for C and ABIInternal. Generated
// entries are raw code addresses even when the external C ABI uses descriptors.
func callWithABI(_ abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr) {
	return call(address, args, workspace), 0
}
func callWithABIArity(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, _ int) (uintptr, uintptr) {
	return callWithABI(flavor, address, args, workspace)
}
