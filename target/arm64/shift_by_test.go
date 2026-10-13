package arm64

import (
	"encoding/binary"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"strings"
	"testing"
)

func TestVariableShiftEncodingAndSize(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, op := range []ir.Opcode{ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpRotateLeft, ir.OpRotateRight} {
			for _, dst := range []machine.Register{0, 1, 2} {
				ins := machine.Instruction{Op: machine.OpShiftBy, Arithmetic: op, Bits: width, A: dst, B: 0, C: 1}
				b := NewByteAssembler(abi.StackNoUse, 0)
				b.ShiftBy(op, width, dst, 0, 1)
				text := NewTextAssembler(abi.StackNoUse, 0)
				text.ShiftBy(op, width, dst, 0, 1)
				if len(b.Bytes()) != PhysicalSize(ins) || len(strings.Split(strings.TrimSpace(text.Text()), "\n"))*4 != len(b.Bytes()) {
					t.Fatalf("size mismatch %v", ins)
				}
				if len(b.Bytes()) > 32 {
					t.Fatalf("shift expanded into %d bytes", len(b.Bytes()))
				}
			}
		}
	}
	b := NewByteAssembler(abi.StackNoUse, 0)
	b.ShiftBy(ir.OpShiftRight, 64, 0, 0, 1)
	// CMP X1,#64; LSRV X0,X0,X1; CSEL X0,X0,XZR,LO.
	want := []uint32{0xf101003f, 0x9ac12400, 0x9a9f3000}
	for i, w := range want {
		if got := binary.LittleEndian.Uint32(b.Bytes()[i*4:]); got != w {
			t.Fatalf("word %d: %#x != %#x", i, got, w)
		}
	}
}
