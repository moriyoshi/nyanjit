package amd64

import (
	"bytes"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

func TestExtensionWidthsAndNativeCopy(t *testing.T) {
	for _, op := range []machine.Opcode{machine.OpNarrow, machine.OpSignExtend} {
		for _, width := range []uint64{0, 7, 33, 65} {
			a := NewByteAssembler(abi.StackNoUse, 0)
			if err := machine.Visit(machine.Instruction{Op: op, A: 0, B: 1, Imm: width}, a); err == nil {
				t.Errorf("op%d width%d accepted", op, width)
			}
		}
		for _, same := range []bool{false, true} {
			src := backend.Register(1)
			if same {
				src = 0
			}
			ins := machine.Instruction{Op: op, A: 0, B: src, Imm: 64}
			a := NewByteAssembler(abi.StackNoUse, 0)
			if err := machine.Visit(ins, a); err != nil {
				t.Fatal(err)
			}
			want := []byte{0x48, 0x89, 0xc8} // MOV RAX,RCX: a 64-bit extension is a copy.
			if same {
				want = nil
			}
			if !bytes.Equal(a.Bytes(), want) || PhysicalSize(ins, abi.StackNoUse, 0) != len(want) {
				t.Fatalf("op%d bytes%x size%d", op, a.Bytes(), PhysicalSize(ins, abi.StackNoUse, 0))
			}
		}
	}
}
