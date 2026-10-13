package arm64_test

import (
	"encoding/binary"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/target/arm64"
	"testing"
)

func TestNEONHighRegistersAndFrameEncodings(t *testing.T) {
	// Golden words independently assembled with GNU aarch64-linux-gnu-as.
	v := machine.VectorRegister
	tests := []struct {
		ins   machine.Instruction
		stack abi.StackPractice
		homes int
		words []uint32
	}{
		{machine.Instruction{Op: machine.OpVectorMove, A: v(31), B: v(16)}, abi.StackNoUse, 0, []uint32{0x4eb01e1f}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpAnd, Bits: 32, A: v(19), B: v(20), C: v(29)}, abi.StackNoUse, 0, []uint32{0x4e3d1e93}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpOr, Bits: 64, A: v(29), B: v(30), C: v(31)}, abi.StackNoUse, 0, []uint32{0x4ebf1fdd}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpXor, Bits: 8, A: v(31), B: v(16), C: v(19)}, abi.StackNoUse, 0, []uint32{0x6e331e1f}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVInsertZero, Bits: 64, A: v(31), B: v(31), C: 12, Imm: 1}, abi.StackNoUse, 0, []uint32{0x4e181d9f}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVLoad, A: v(16), B: 13}, abi.StackNoUse, 0, []uint32{0x3dc001b0}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVStore, A: 12, B: v(31)}, abi.StackNoUse, 0, []uint32{0x3d80019f}},
		{machine.Instruction{Op: machine.OpVectorSpill, A: v(19), Slot: 15}, abi.StackPlatform, 0, []uint32{0x3d8023f3}},
		{machine.Instruction{Op: machine.OpVectorReload, A: v(29), Slot: 10}, abi.StackPlatform, 9, []uint32{0x3dc02bfd}},
		{machine.Instruction{Op: machine.OpVectorSpill, A: v(16), Slot: 18}, abi.StackNoUse, 0, []uint32{0x3d802510}},
		{machine.Instruction{Op: machine.OpVectorReload, A: v(31), Slot: 18}, abi.StackNoUse, 0, []uint32{0x3dc0251f}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVMaskBits, A: 12, B: v(29)}, abi.StackNoUse, 0, []uint32{0xd2804030, 0xf2a10090, 0xf2c40210, 0xf2f00810, 0x4e080e1e, 0x4e3e1fbe, 0x6e1e43df, 0x4e1f3bde, 0x4e71bbde, 0x0e023fcc}},
	}
	for _, tc := range tests {
		b := arm64.NewByteAssembler(tc.stack, tc.homes)
		text := arm64.NewTextAssembler(tc.stack, tc.homes)
		if err := b.Vector(tc.ins); err != nil {
			t.Fatal(err)
		}
		if err := text.Vector(tc.ins); err != nil {
			t.Fatal(err)
		}
		data := b.Bytes()
		if len(data) != len(tc.words)*4 {
			t.Fatalf("%+v got%x", tc.ins, data)
		}
		for i, w := range tc.words {
			if got := binary.LittleEndian.Uint32(data[i*4:]); got != w {
				t.Fatalf("%+v word%d got%#x want%#x", tc.ins, i, got, w)
			}
		}
		if text.AssembledSize() != len(data) || arm64.PhysicalSizeWithFrame(tc.ins, tc.stack, tc.homes) != len(data) {
			t.Fatal("frame-aware sizing disagrees")
		}
	}
	for n := uint8(16); n < 32; n++ {
		b := arm64.NewByteAssembler(abi.StackNoUse, 0)
		ins := machine.Instruction{Op: machine.OpVector, Arithmetic: ir.OpVLoad, A: v(n), B: 13}
		if err := b.Vector(ins); err != nil {
			t.Fatal(err)
		}
		if got := binary.LittleEndian.Uint32(b.Bytes()); got != 0x3dc001a0|uint32(n) {
			t.Fatalf("Q%d encoding=%#x", n, got)
		}
	}
}
