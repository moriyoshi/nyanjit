package i386_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
)

func TestFloatMemory(t *testing.T) {
	for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
		for _, width := range []uint8{32, 64} {
			for _, reload := range []bool{false, true} {
				op, opcode := machine.OpFloatSpill, byte(0x11)
				if reload {
					op, opcode = machine.OpFloatReload, 0x10
				}
				prefix, mnemonic := byte(0xf2), "movsd"
				if width == 32 {
					prefix, mnemonic = 0xf3, "movss"
				}
				i := machine.Instruction{Op: op, A: backend.VectorRegister(7), Bits: width, Slot: 1}
				want := []byte{prefix, 0x0f, opcode, 0xbd, 4, 0, 0, 0}
				base := "EBP"
				if stack == abi.StackPlatform {
					want = []byte{prefix, 0x0f, opcode, 0xbc, 0x24, 4, 0, 0, 0}
					base = "ESP"
				}
				a := i386.NewByteAssembler(stack)
				x := i386.NewTextAssembler(stack)
				if e := machine.Visit(i, a); e != nil {
					t.Fatal(e)
				}
				if e := machine.Visit(i, x); e != nil {
					t.Fatal(e)
				}
				if !bytes.Equal(a.Bytes(), want) || a.AssembledSize() != x.AssembledSize() || i386.PhysicalSize(i, stack, 0) != len(want) {
					t.Fatalf("width%d stack%d reload%t code=%x want%x", width, stack, reload, a.Bytes(), want)
				}
				if !strings.Contains(x.Text(), mnemonic) || !strings.Contains(x.Text(), "["+base+" + 4]") {
					t.Fatal(x.Text())
				}
				if i386.FlagEffect(i) {
					t.Fatal("SSE spill/reload clobbers integer flags")
				}
			}
		}
	}
}
