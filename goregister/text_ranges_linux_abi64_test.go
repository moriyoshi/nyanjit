//go:build linux && (amd64 || arm64 || riscv64 || ppc64 || ppc64le)

package goregister

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
)

func TestRegisterRejectsTouchingTextRanges(t *testing.T) {
	if !Supported() {
		t.Skip("runtime metadata unsupported")
	}
	page := os.Getpagesize()
	mem, err := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	switch runtime.GOARCH {
	case "amd64":
		mem[0] = 0xc3
		mem[page] = 0xc3
	case "arm64":
		copy(mem, []byte{0xc0, 0x03, 0x5f, 0xd6})
		copy(mem[page:], mem[:4])
	case "riscv64":
		copy(mem, []byte{0x67, 0x80, 0, 0})
		copy(mem[page:], mem[:4])
	case "ppc64":
		copy(mem, []byte{0x4e, 0x80, 0, 0x20})
		copy(mem[page:], mem[:4])
	case "ppc64le":
		copy(mem, []byte{0x20, 0, 0x80, 0x4e})
		copy(mem[page:], mem[:4])
	}
	if err := syscall.Mprotect(mem, syscall.PROT_READ|syscall.PROT_EXEC); err != nil {
		syscall.Munmap(mem)
		t.Fatal(err)
	}
	frame := Frame{CodeBytes: page, PointerFree: true, SP: []SPPoint{{PC: 0, Delta: 0}}}
	linked, err := Register(mem[:page], frame)
	if err != nil || !linked {
		if !linked {
			syscall.Munmap(mem)
		}
		t.Fatalf("first registration: linked=%t %v", linked, err)
	}
	// The first page is now retained by the runtime; the mapping stays allocated.
	linked, err = Register(mem[page:], frame)
	if linked || !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("touching registration: linked=%t err=%v", linked, err)
	}
}
