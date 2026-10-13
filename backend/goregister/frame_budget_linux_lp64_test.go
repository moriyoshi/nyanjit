//go:build !nyanjit_no_goregister && linux && (loong64 || riscv64)

package goregister

import (
	"errors"
	"runtime"
	"syscall"
	"testing"
)

func TestLP64ManagedFrameBoundary(t *testing.T) {
	if !Supported() {
		t.Skip("unsupported Go runtime")
	}
	code := []byte{0x20, 0, 0, 0x4c} // LoongArch ret.
	if runtime.GOARCH == "riscv64" {
		code = []byte{0x67, 0x80, 0, 0}
	}
	for _, size := range []int{624, 640, 656} {
		info := Frame{CodeBytes: len(code), FrameBytes: size, SP: []SPPoint{{PC: 0, Delta: 0}}, PointerFree: true}
		m, err := newJITModule(layoutForVersion(runtime.Version()), code, info)
		if size > 640 {
			if m != nil || !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("frame %d: module=%v error=%v, want ErrInvalidFrame", size, m, err)
			}
		} else {
			if err != nil {
				t.Fatalf("frame %d: %v", size, err)
			}
			m.pinner.Unpin()
			if err := syscall.Munmap(m.storage); err != nil {
				t.Fatal(err)
			}
		}
	}
}
