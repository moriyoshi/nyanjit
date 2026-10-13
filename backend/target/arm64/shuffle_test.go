package arm64_test

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
)

// shuffleForm builds an OpVShuffle of bits-wide lanes with lane i's index
// in byte i of Imm, then of Disp.
func shuffleForm(bits uint8, a, b, c uint8, indices []int) machine.Instruction {
	v := backend.VectorRegister
	ins := machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVShuffle, Bits: bits, A: v(a), B: v(b), C: v(c)}
	for i, x := range indices {
		if i < 8 {
			ins.Imm |= uint64(x) << (8 * i)
		} else {
			ins.Disp |= uint64(x) << (8 * (i - 8))
		}
	}
	return ins
}

func lanes(n int, f func(i int) int) []int {
	x := make([]int, n)
	for i := range x {
		x[i] = f(i)
	}
	return x
}

func TestNEONShuffleGNUAssemblerWords(t *testing.T) {
	// Independently assembled using GNU as on the arm64 host.
	v := backend.VectorRegister
	for _, test := range []struct {
		ins   machine.Instruction
		words []uint32
	}{
		{shuffleForm(32, 3, 4, 5, lanes(4, func(i int) int { return 4 + i })), []uint32{0x4ea51ca3}},                                                                                                                          // mov V3, V5
		{shuffleForm(32, 4, 4, 5, lanes(4, func(i int) int { return i })), nil},                                                                                                                                               // in place
		{shuffleForm(16, 3, 4, 5, lanes(8, func(int) int { return 11 })), []uint32{0x4e0e04a3}},                                                                                                                               // dup V3.8H, V5.H[3]
		{shuffleForm(8, 3, 4, 5, lanes(16, func(i int) int { return i/2 + i%2*16 })), []uint32{0x4e053883}},                                                                                                                   // zip1 16B
		{shuffleForm(64, 3, 4, 5, lanes(2, func(i int) int { return 1 + i*2 })), []uint32{0x4ec57883}},                                                                                                                        // zip2 2D
		{shuffleForm(32, 3, 4, 5, lanes(4, func(i int) int { return 2*i + 1 })), []uint32{0x4e855883}},                                                                                                                        // uzp2 4S
		{shuffleForm(16, 3, 4, 5, lanes(8, func(i int) int { return i&^1 + i%2*8 })), []uint32{0x4e452883}},                                                                                                                   // trn1 8H
		{shuffleForm(8, 3, 4, 5, lanes(16, func(i int) int { return (i + 19) % 32 })), []uint32{0x6e0418a3}},                                                                                                                  // ext V5, V4, #3
		{shuffleForm(64, 3, 4, 4, lanes(2, func(i int) int { return 1 - i })), []uint32{0x6e044083}},                                                                                                                          // ext V4, V4, #8
		{shuffleForm(8, 3, 4, 5, lanes(16, func(i int) int { return 15 - i })), []uint32{0xd281c1f0, 0xf2a181b0, 0xf2c14170, 0xf2e10130, 0x4e081e1f, 0xd280c0f0, 0xf2a080b0, 0xf2c04070, 0xf2e00030, 0x4e181e1f, 0x4e1f0083}}, // tbl from V4
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtendUnsigned, Bits: 16, A: v(3), B: v(4)}, []uint32{0x2f08a483}},
		{machine.Instruction{Op: machine.OpVector, Arithmetic: op.OpVExtendSigned, Bits: 64, A: v(3), B: v(4), Imm: 1}, []uint32{0x4f20a483}},
	} {
		bytes := arm64.NewByteAssembler(abi.StackNoUse, 0)
		text := arm64.NewTextAssembler(abi.StackNoUse, 0)
		if err := bytes.Vector(test.ins); err != nil {
			t.Fatalf("%+v: %v", test.ins, err)
		}
		if err := text.Vector(test.ins); err != nil {
			t.Fatal(err)
		}
		code := bytes.Bytes()
		if len(code) != 4*len(test.words) {
			t.Fatalf("%+v: %d bytes, want %d words", test.ins, len(code), len(test.words))
		}
		for i, w := range test.words {
			if got := binary.LittleEndian.Uint32(code[4*i:]); got != w {
				t.Fatalf("%+v word %d: %#x, want %#x", test.ins, i, got, w)
			}
		}
		if text.AssembledSize() != len(code) || arm64.VectorSize(test.ins, abi.StackNoUse, 0) != len(code) {
			t.Fatalf("%+v: SIMD PC sizing mismatch", test.ins)
		}
	}
}

// Every valid index set encodes, VectorSize agrees with the bytes, and an
// invalid set is rejected.
func TestNEONShuffleSizes(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 0x5f))
	for n := 0; n < 20000; n++ {
		bits := []uint8{8, 16, 32, 64}[r.IntN(4)]
		count := 128 / int(bits)
		indices := lanes(count, func(int) int { return r.IntN(2 * count) })
		if n%5 == 0 {
			k := r.IntN(2 * count)
			indices = lanes(count, func(i int) int { return (i + k) % (2 * count) })
		}
		regs := [3]uint8{uint8(r.IntN(30)), uint8(r.IntN(30)), uint8(r.IntN(30))}
		ins := shuffleForm(bits, regs[0], regs[1], regs[2], indices)
		a := arm64.NewByteAssembler(abi.StackNoUse, 0)
		if err := a.Vector(ins); err != nil {
			t.Fatalf("%+v: %v", ins, err)
		}
		if size := arm64.VectorSize(ins, abi.StackNoUse, 0); size != len(a.Bytes()) {
			t.Fatalf("%+v: size %d, encoded %d", ins, size, len(a.Bytes()))
		}
	}
	v := backend.VectorRegister
	for _, ins := range []machine.Instruction{
		shuffleForm(32, 3, 4, 5, []int{0, 1, 2, 8}),
		shuffleForm(32, 3, 4, 5, []int{0, 1, 2, 3, 1}),
		{Op: machine.OpVector, Arithmetic: op.OpVShuffle, Bits: 64, A: v(3), B: v(4), C: v(5), Imm: 0x0100, Disp: 1},
		shuffleForm(32, 3, 30, 5, []int{0, 1, 2, 3}),
		{Op: machine.OpVector, Arithmetic: op.OpVExtendUnsigned, Bits: 8, A: v(3), B: v(4)},
		{Op: machine.OpVector, Arithmetic: op.OpVExtendSigned, Bits: 32, A: v(3), B: v(4), Imm: 2},
	} {
		if err := arm64.NewByteAssembler(abi.StackNoUse, 0).Vector(ins); err == nil || arm64.VectorSize(ins, abi.StackNoUse, 0) != 0 {
			t.Fatalf("%+v: accepted", ins)
		}
	}
}
