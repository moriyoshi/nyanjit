package amd64_test

import (
	"bytes"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/amd64"
	"testing"
)

func TestSSE2VectorEncodingAndPCs(t *testing.T) {
	v := machine.VectorRegister
	for _, test := range []struct {
		ins  machine.Instruction
		want []byte
	}{
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVLoad, A: v(3), B: 6}, []byte{0xf3, 0x0f, 0x6f, 0x1e}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVStore, A: 7, B: v(3)}, []byte{0xf3, 0x0f, 0x7f, 0x1f}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpAdd, Bits: 32, A: v(3), B: v(3), C: v(4)}, []byte{0x66, 0x0f, 0xfe, 0xdc}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVLoad, A: v(11), B: 13}, []byte{0xf3, 0x45, 0x0f, 0x6f, 0x5d, 0}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVMaskBits, A: 12, B: v(11)}, []byte{0x66, 0x45, 0x0f, 0xd7, 0xe3}},
	} {
		binary := amd64.NewByteAssembler(abi.StackNoUse, 0)
		text := amd64.NewTextAssembler(abi.StackNoUse, 0)
		if err := binary.Vector(test.ins); err != nil {
			t.Fatal(err)
		}
		if err := text.Vector(test.ins); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(binary.Bytes(), test.want) {
			t.Fatalf("%+v =%x;want%x", test.ins, binary.Bytes(), test.want)
		}
		if len(test.want) != text.AssembledSize() || len(test.want) != amd64.VectorSize(test.ins, abi.StackNoUse, 0) {
			t.Fatal("SIMD emitter/byte/text sizes diverge")
		}
		if amd64.FlagEffect(test.ins) {
			t.Fatal("integer SIMD changes GPR flags")
		}
	}
}
