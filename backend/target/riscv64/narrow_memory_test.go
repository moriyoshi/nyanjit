package riscv64

import (
	"encoding/binary"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestNarrowMemoryEncoding(t *testing.T) {
	ops := []machine.Opcode{machine.OpLoad16, machine.OpStore16, machine.OpLoad32, machine.OpStore32}
	want := []uint32{0x15083, 0x209023, 0x16083, 0x20a023}
	for k, operation := range ops {
		e := NewByteAssembler(abi.StackNoUse, 0)
		i := machine.Instruction{Op: operation, A: 1, B: 2}
		if err := machine.Visit(i, e); err != nil {
			t.Fatal(err)
		}
		code := e.Bytes()
		if len(code) != 4 || binary.LittleEndian.Uint32(code) != want[k] {
			t.Fatalf("op%d got%x want%08x", operation, code, want[k])
		}
		i.ByteOrder = op.ByteOrderBigEndian
		if err := machine.Visit(i, e); err == nil {
			t.Fatal("big endian must be legalized")
		}
	}
}
