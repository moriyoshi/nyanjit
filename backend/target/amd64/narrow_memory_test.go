package amd64

import (
	"bytes"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestNarrowMemoryEncoding(t *testing.T) {
	for _, tt := range []struct {
		op   machine.Opcode
		a, b backend.Register
		want []byte
	}{
		{machine.OpLoad16, 1, 2, []byte{0x0f, 0xb7, 0x0a}},
		{machine.OpLoad32, 1, 2, []byte{0x8b, 0x0a}},
		{machine.OpStore16, 1, 2, []byte{0x66, 0x89, 0x11}},
		{machine.OpStore32, 1, 2, []byte{0x89, 0x11}},
		{machine.OpLoad16, 6, 4, []byte{0x0f, 0xb7, 0x34, 0x24}},
		{machine.OpLoad32, 7, 5, []byte{0x8b, 0x7d, 0}},
		{machine.OpStore16, 4, 7, []byte{0x66, 0x89, 0x3c, 0x24}},
		{machine.OpStore32, 5, 6, []byte{0x89, 0x75, 0}},
		{machine.OpLoad16, 9, 13, []byte{0x45, 0x0f, 0xb7, 0x4d, 0}},
		{machine.OpLoad32, 10, 12, []byte{0x45, 0x8b, 0x14, 0x24}},
		{machine.OpStore16, 13, 9, []byte{0x66, 0x45, 0x89, 0x4d, 0}},
		{machine.OpStore32, 12, 10, []byte{0x45, 0x89, 0x14, 0x24}},
	} {
		e := NewByteAssembler(abi.StackNoUse, 0)
		i := machine.Instruction{Op: tt.op, A: tt.a, B: tt.b}
		if err := machine.Visit(i, e); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(e.Bytes(), tt.want) {
			t.Fatalf("%+v got%x want%x", i, e.Bytes(), tt.want)
		}
		if size := PhysicalSize(i, abi.StackNoUse, 0); size != len(tt.want) {
			t.Fatalf("size%d bytes%d", size, len(tt.want))
		}
		i.ByteOrder = op.ByteOrderBigEndian
		if err := machine.Visit(i, e); err == nil {
			t.Fatal("big endian must be legalized")
		}
	}
}
