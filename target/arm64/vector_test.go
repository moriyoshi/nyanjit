package arm64_test

import (
	"encoding/binary"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/arm64"
	"testing"
)

func TestNEONVectorGNUAssemblerWords(t *testing.T) {
	// Independently assembled using GNU as on the arm64 host.
	v := machine.VectorRegister
	for _, test := range []struct {
		ins  machine.Instruction
		word uint32
	}{
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVLoad, A: v(3), B: 1}, 0x3dc00023},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVStore, A: 1, B: v(3)}, 0x3d800023},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpAdd, Bits: 32, A: v(3), B: v(4), C: v(5)}, 0x4ea58483},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpSub, Bits: 64, A: v(3), B: v(4), C: v(5)}, 0x6ee58483},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpMul, Bits: 16, A: v(3), B: v(4), C: v(5)}, 0x4e659c83},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVSplat, Bits: 8, A: v(3), B: 1}, 0x4e010c23},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVExtract, Bits: 8, A: 1, B: v(3), Imm: 7}, 0x0e0f3c61},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVExtract, Bits: 64, A: 1, B: v(3), Imm: 1}, 0x4e183c61},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVInsert, Bits: 16, A: v(3), B: v(3), C: 1, Imm: 7}, 0x4e1e1c23},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVCompare, Bits: 8, A: v(3), B: v(4), C: v(5)}, 0x6e258c83},
	} {
		bytes := arm64.NewByteAssembler(abi.StackNoUse, 0)
		text := arm64.NewTextAssembler(abi.StackNoUse, 0)
		if err := bytes.Vector(test.ins); err != nil {
			t.Fatal(err)
		}
		if err := text.Vector(test.ins); err != nil {
			t.Fatal(err)
		}
		if len(bytes.Bytes()) != 4 || binary.LittleEndian.Uint32(bytes.Bytes()) != test.word {
			t.Fatalf("%+v got%x;want%#x", test.ins, bytes.Bytes(), test.word)
		}
		if text.AssembledSize() != 4 || arm64.VectorSize(test.ins, abi.StackNoUse, 0) != 4 {
			t.Fatal("SIMD PC sizing mismatch")
		}
	}
}
