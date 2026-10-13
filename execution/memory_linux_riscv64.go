//go:build linux && riscv64

package execution

import (
	"syscall"
	"unsafe"

	"github.com/moriyoshi/nyanjit/abi"
)

// Linux riscv_flush_icache synchronizes instruction caches across threads.
const riscvFlushICache = 259

func mapCode(code []byte) ([]byte, error) {
	mem, err := syscall.Mmap(-1, 0, len(code), syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, err
	}
	copy(mem, code)
	if err := syscall.Mprotect(mem, syscall.PROT_READ|syscall.PROT_EXEC); err != nil {
		_ = syscall.Munmap(mem)
		return nil, err
	}
	start := uintptr(unsafe.Pointer(&mem[0]))
	_, _, errno := syscall.RawSyscall(riscvFlushICache, start, start+uintptr(len(code)), 0)
	if errno != 0 {
		_ = syscall.Munmap(mem)
		return nil, errno
	}
	return mem, nil
}
func unmapCode(mem []byte) error { return syscall.Munmap(mem) }
func entry(mem []byte) uintptr   { return uintptr(unsafe.Pointer(&mem[0])) }
func call(address uintptr, args *ArgumentWords, workspace uintptr) uintptr
func callGo(address uintptr, args *ArgumentWords, workspace uintptr) uintptr
func callPair(address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr)
func callWithABI(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr) (uintptr, uintptr) {
	if flavor == abi.Go {
		return callGo(address, args, workspace), 0
	}
	return call(address, args, workspace), 0
}
func callWithABIArity(flavor abi.Flavor, address uintptr, args *ArgumentWords, workspace uintptr, _ int) (uintptr, uintptr) {
	return callWithABI(flavor, address, args, workspace)
}
