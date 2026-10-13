package riscv64

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestVariableShiftEncodingAndSize(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, op := range []op.Opcode{op.OpShiftLeft, op.OpShiftRight, op.OpShiftRightArithmetic, op.OpRotateLeft, op.OpRotateRight} {
			for _, dst := range []backend.Register{10, 11, 12} {
				ins := machine.Instruction{Op: machine.OpShiftBy, Arithmetic: op, Bits: width, A: dst, B: 10, C: 11}
				b := NewByteAssembler(abi.StackNoUse, 0)
				b.ShiftBy(op, width, dst, 10, 11)
				text := NewTextAssembler(abi.StackNoUse, 0)
				text.ShiftBy(op, width, dst, 10, 11)
				if len(b.Bytes()) != PhysicalSize(ins, abi.StackNoUse, 0) || len(strings.Split(strings.TrimSpace(text.Text()), "\n"))*4 != len(b.Bytes()) {
					t.Fatalf("size mismatch %v", ins)
				}
				if strings.Contains(text.Text(), ".word") {
					t.Fatalf("undecoded instruction: %s", text.Text())
				}
				if len(b.Bytes()) > 48 {
					t.Fatalf("shift expanded into %d bytes", len(b.Bytes()))
				}
			}
		}
	}
	b := NewByteAssembler(abi.StackNoUse, 0)
	b.ShiftBy(op.OpShiftRight, 64, 10, 10, 11)
	want := []uint32{0x0405bf13, 0x41e00f33, 0x00b55fb3, 0x01eff533}
	for i, w := range want {
		if got := binary.LittleEndian.Uint32(b.Bytes()[i*4:]); got != w {
			t.Fatalf("word %d: %#x != %#x", i, got, w)
		}
	}
}
