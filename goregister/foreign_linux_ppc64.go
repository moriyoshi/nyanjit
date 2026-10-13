//go:build linux && ppc64

package goregister

import (
	"runtime"
	"sync/atomic"
	"unsafe"
)

// ppc64MCallState mirrors only the fields needed for a runtime-originated
// call transition. Offsets are from runtime's generated go_asm.h on
// linux/ppc64 in Go 1.26.9 and 1.27.2; both release families agree.
type ppc64MCallState struct {
	G0          unsafe.Pointer // 0
	_           [56]byte
	ProcID      uint64 // 64
	_           [112]byte
	Curg        unsafe.Pointer // 184
	_           [88]byte
	InC         bool // 280
	_           [39]byte
	TotalCalls  uint64 // 320
	ActiveCalls int32  // 328
	_           uint32
	Callers     *[32]uintptr // 336
}

type ppc64SystemCall func(fn, arg unsafe.Pointer) int32

var ppc64SystemCallEntry = func() ppc64SystemCall {
	address, err := RuntimeEntry("runtime.asmcgocall")
	if err != nil {
		return nil
	}
	cell := &struct{ entry uintptr }{address}
	pointer := unsafe.Pointer(cell)
	return *(*ppc64SystemCall)(unsafe.Pointer(&pointer))
}()

//go:noescape
func foreignCurrentThread() (mp *ppc64MCallState, gp unsafe.Pointer)

func foreignHasCgo() bool

//go:linkname foreignRuntimeCgoCall runtime.cgocall
func foreignRuntimeCgoCall(fn, arg unsafe.Pointer) int32

//go:linkname foreignEnterSyscall runtime.entersyscall
func foreignEnterSyscall()

//go:linkname foreignExitSyscall runtime.exitsyscall
func foreignExitSyscall()

// ForeignCallSupported reports whether ForeignCall has a validated runtime
// transition implementation. Currently this facility is for linux/ppc64.
func ForeignCallSupported() bool { return Supported() && ppc64SystemCallEntry != nil }

// ForeignCall enters a C ABI function on the runtime's system stack. fn must
// use the executing Go binary's ELF pointer convention; arg is its single
// frame pointer. Callbacks must come back through that same call chain.
// This maintains per-thread cgo accounting without installing fakecgo or
// changing runtime.iscgo. The adapter must recover g0 before cgocallback.
//
//go:nosplit
func ForeignCall(fn, arg unsafe.Pointer) int32 {
	if !ForeignCallSupported() || fn == nil {
		panic(ErrUnsupported)
	}
	if foreignHasCgo() {
		return foreignRuntimeCgoCall(fn, arg)
	}
	mp, gp := foreignCurrentThread()
	if mp == nil || mp.G0 == nil || mp.Curg != gp || mp.InC || mp.ActiveCalls < 0 {
		panic(ErrUnsupported)
	}
	atomic.AddUint64(&mp.TotalCalls, 1)
	if mp.Callers != nil {
		mp.Callers[0] = 0
	}
	foreignEnterSyscall()
	mp.InC = true
	mp.ActiveCalls++
	result := ppc64SystemCallEntry(fn, arg)
	// A successful callback returns to this same M. Unwinding a panic instead
	// goes through runtime.unwindm, which owns the matching accounting cleanup.
	mp.InC = false
	mp.ActiveCalls--
	foreignExitSyscall()
	// Keep arguments alive at both saved syscall PCs when a callback grows
	// the Go stack and the runtime restores the earlier syscall state.
	runtime.KeepAlive(fn)
	runtime.KeepAlive(arg)
	runtime.KeepAlive(mp)
	return result
}
