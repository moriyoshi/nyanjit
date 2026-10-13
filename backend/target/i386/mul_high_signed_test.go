package i386

import (
	"bytes"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestMulHighSignedEncoding(t *testing.T) {
	// mov eax, ebx; imul esi; mov ecx, edx
	e := NewByteAssembler(abi.StackPlatform)
	e.MulHighSigned(32, 1, 3, 6)
	if want := []byte{0x89, 0xd8, 0xf7, 0xee, 0x89, 0xd1}; e.Err() != nil || !bytes.Equal(e.code, want) {
		t.Fatalf("% x, %v; want % x", e.code, e.Err(), want)
	}
	text := NewTextAssembler(abi.StackPlatform)
	text.MulHighSigned(32, 2, 0, 1)
	if listing := text.Text(); !bytes.Contains([]byte(listing), []byte("imul ECX")) || bytes.Contains([]byte(listing), []byte("mov")) {
		t.Fatalf("listing %q", listing)
	}
	for _, bad := range [][4]uint8{{32, 1, 3, 0}, {16, 1, 3, 6}} {
		e := NewByteAssembler(abi.StackPlatform)
		e.MulHighSigned(bad[0], backend.Register(bad[1]), backend.Register(bad[2]), backend.Register(bad[3]))
		if e.Err() == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}
