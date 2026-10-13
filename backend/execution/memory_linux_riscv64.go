//go:build linux && riscv64

package execution

import (
	"syscall"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// Linux riscv_flush_icache synchronizes instruction caches across threads.
const riscvFlushICache = 259

// makeExecutable seals mem read-only and executable after its first n bytes
// were written.
func makeExecutable(mem []byte, n int) error {
	if err := syscall.Mprotect(mem, syscall.PROT_READ|syscall.PROT_EXEC); err != nil {
		return err
	}
	start := uintptr(unsafe.Pointer(&mem[0]))
	if _, _, errno := syscall.RawSyscall(riscvFlushICache, start, start+uintptr(n), 0); errno != 0 {
		return errno
	}
	return nil
}
func entry(mem []byte) uintptr { return uintptr(unsafe.Pointer(&mem[0])) }
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
