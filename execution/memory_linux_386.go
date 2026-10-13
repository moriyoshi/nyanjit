//go:build linux && 386

package execution

import (
	"syscall"
	"unsafe"

	"github.com/moriyoshi/nyanjit/abi"
)

func mapCode(code []byte) ([]byte, error) {
	mem, err := syscall.Mmap(-1, 0, len(code), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, err
	}
	copy(mem, code)
	if err := syscall.Mprotect(mem, syscall.PROT_READ|syscall.PROT_EXEC); err != nil {
		_ = syscall.Munmap(mem)
		return nil, err
	}
	return mem, nil
}

func unmapCode(mem []byte) error { return syscall.Munmap(mem) }
func entry(mem []byte) uintptr   { return uintptr(unsafe.Pointer(&mem[0])) }
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
