package amd64

import (
	"bytes"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// The bytes disassemble (Go objdump) to the listed instructions.
func TestMulHighSignedEncoding(t *testing.T) {
	for _, test := range []struct {
		w       uint8
		d, a, b backend.Register
		code    []byte
	}{
		// movsx r11, cl; movsx rbx, sil; imul rbx, r11; shr rbx, 8; movzx ebx, bl
		{8, 3, 6, 1, []byte{0x4c, 0x0f, 0xbe, 0xd9, 0x48, 0x0f, 0xbe, 0xde, 0x49, 0x0f, 0xaf, 0xdb, 0x48, 0xc1, 0xeb, 0x08, 0x40, 0x0f, 0xb6, 0xdb}},
		// movsxd r11, r12d; movsxd r8, r15d; imul r8, r11; shr r8, 32
		{32, 8, 15, 12, []byte{0x4d, 0x63, 0xdc, 0x4d, 0x63, 0xc7, 0x4d, 0x0f, 0xaf, 0xc3, 0x49, 0xc1, 0xe8, 0x20}},
		// mov rax, r15; imul r12; mov r8, rdx
		{64, 8, 15, 12, []byte{0x4c, 0x89, 0xf8, 0x49, 0xf7, 0xec, 0x49, 0x89, 0xd0}},
		// imul rcx alone when B is RAX and A is RDX
		{64, 2, 0, 1, []byte{0x48, 0xf7, 0xe9}},
	} {
		e := NewByteAssembler(abi.StackPlatform, 0)
		e.MulHighSigned(test.w, test.d, test.a, test.b)
		if e.Err() != nil || !bytes.Equal(e.code, test.code) {
			t.Errorf("%d bits A=%d B=%d C=%d: % x, %v; want % x", test.w, test.d, test.a, test.b, e.code, e.Err(), test.code)
		}
		ins := machine.Instruction{Op: machine.OpMulHighSigned, Bits: test.w, A: test.d, B: test.a, C: test.b}
		if PhysicalSize(ins, abi.StackPlatform, 0) != len(test.code) || !FlagEffect(ins) {
			t.Errorf("%d bits: size %d, flag effect %t", test.w, PhysicalSize(ins, abi.StackPlatform, 0), FlagEffect(ins))
		}
	}
	for _, bad := range []machine.Instruction{
		{Op: machine.OpMulHighSigned, Bits: 64, A: 3, B: 6, C: 0},
		{Op: machine.OpMulHighSigned, Bits: 32, A: 3, B: 11, C: 1},
		{Op: machine.OpMulHighSigned, Bits: 12, A: 3, B: 6, C: 1},
	} {
		e := NewByteAssembler(abi.StackPlatform, 0)
		e.MulHighSigned(bad.Bits, bad.A, bad.B, bad.C)
		if e.Err() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
