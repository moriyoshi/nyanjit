//go:build linux && ppc64

package goregister

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

func TestForeignCallStateMatchesRuntimeLayout(t *testing.T) {
	if !ForeignCallSupported() {
		t.Skip("unsupported runtime")
	}
	if unsafe.Offsetof(ppc64MCallState{}.ProcID) != 64 ||
		unsafe.Offsetof(ppc64MCallState{}.Curg) != 184 ||
		unsafe.Offsetof(ppc64MCallState{}.InC) != 280 ||
		unsafe.Offsetof(ppc64MCallState{}.TotalCalls) != 320 ||
		unsafe.Offsetof(ppc64MCallState{}.ActiveCalls) != 328 ||
		unsafe.Offsetof(ppc64MCallState{}.Callers) != 336 {
		t.Fatal("foreign-call runtime mirror offsets changed")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	mp, gp := foreignCurrentThread()
	if mp == nil || mp.G0 == nil || mp.Curg != gp || mp.InC || mp.ActiveCalls != 0 {
		t.Fatalf("invalid running-thread state: %p %p", mp, gp)
	}
	if mp.ProcID != uint64(syscall.Gettid()) {
		t.Fatalf("runtime thread id=%d; kernel=%d", mp.ProcID, syscall.Gettid())
	}
}
