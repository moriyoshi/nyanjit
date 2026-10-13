//go:build linux && (loong64 || riscv64)

package execution

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

// alignedForeignCall is project-owned; it does not depend on purego's private
// syscall block. Our C shim runs after the runtime transition on g0, establishes
// an aligned frame, forwards eight register arguments and captures both results.
// Go's asmcgocall may hand the C side an SP that is only eight-byte aligned.
// Fields are uintptr because this is a pointer-free C argument/result block.
type alignedForeignCall struct {
	target    uintptr
	args      ArgumentWords
	low, high uintptr
}

func alignedForeignEntry() uintptr

var alignedForeignAddress = alignedForeignEntry()

func callForeign(address uintptr, args []uintptr) (uintptr, uintptr) {
	if len(args) > MaxArgumentWords {
		panic(ErrArity)
	}
	if address == 0 {
		panic("execution: nil foreign function")
	}
	frame := &alignedForeignCall{target: address}
	copy(frame.args[:], args)
	// SyscallN's uintptrescapes directive makes this block heap resident. Retain
	// it through callbacks, GC and Go stack growth inside the foreign call.
	purego.SyscallN(alignedForeignAddress, uintptr(unsafe.Pointer(frame)))
	runtime.KeepAlive(frame)
	return frame.low, frame.high
}
