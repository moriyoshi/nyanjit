//go:build linux && loong64

package execution

import (
	"syscall"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// syncICache executes IBAR 0 before exposing newly written code. This follows
// Linux/LoongArch's coherent instruction/data cache model: the RX transition
// publishes stores, and each call trampoline executes IBAR on the entering
// thread. This is publication of immutable code, not a live-patching protocol.
func syncICache()

// makeExecutable seals mem read-only and executable after its first n bytes
// were written.
func makeExecutable(mem []byte, _ int) error {
	if err := syscall.Mprotect(mem, syscall.PROT_READ|syscall.PROT_EXEC); err != nil {
		return err
	}
	syncICache()
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
