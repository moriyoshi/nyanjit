//go:build linux && (arm64 || amd64)

package goregister

import (
	"errors"
	"runtime"
	"testing"
)

func TestLayoutForVersion(t *testing.T) {
	for _, test := range []struct {
		version string
		want    runtimeLayout
	}{
		{"go1.26", layout126},
		{"go1.26.0", layout126},
		{"go1.26.9", layout126},
		{"go1.27", layout127},
		{"go1.27.2", layout127},
		{"go1.25.9", layoutUnknown},
		{"go1.28.0", layoutUnknown},
		{"go1.27rc1", layoutUnknown},
		{"devel go1.27", layoutUnknown},
	} {
		if got := layoutForVersion(test.version); got != test.want {
			t.Errorf("layoutForVersion(%q) = %d, want %d", test.version, got, test.want)
		}
	}
}

func TestRegisterRejectsInvalidFrame(t *testing.T) {
	if !Supported() {
		t.Skip("runtime module layout is unsupported")
	}
	code := []byte{0xc3} // RET on amd64.
	if runtime.GOARCH == "arm64" {
		code = []byte{0xc0, 0x03, 0x5f, 0xd6}
	}
	base := Frame{CodeBytes: len(code), FrameBytes: 16, SP: []SPPoint{{PC: 0, Delta: 0}}, PointerFree: true}
	tests := []struct {
		name string
		edit func(*Frame)
	}{
		{"code length", func(f *Frame) { f.CodeBytes++ }},
		{"frame exceeds nosplit reserve", func(f *Frame) { f.FrameBytes = 656 }},
		{"negative frame", func(f *Frame) { f.FrameBytes = -16 }},
		{"unaligned frame", func(f *Frame) { f.FrameBytes = 4 }},
		{"stack delta beyond frame", func(f *Frame) { f.SP[0].Delta = 32 }},
		{"missing stack points", func(f *Frame) { f.SP = nil }},
		{"name with NUL", func(f *Frame) { f.Name = "bad\x00name" }},
		{"missing pointer-free assertion", func(f *Frame) { f.PointerFree = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			frame := base
			frame.SP = append([]SPPoint(nil), base.SP...)
			test.edit(&frame)
			linked, err := Register(code, frame)
			if linked || !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("Register = (%t, %v), want (false, ErrInvalidFrame)", linked, err)
			}
		})
	}
}
