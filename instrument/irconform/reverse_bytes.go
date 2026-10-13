package irconform

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// reverseBytesCases cover OpReverseBytes at each width, the 128-bit Builder
// form, and big-endian 64-bit loads and stores, which reverse the bytes of a
// little-endian access, at plain, offset and indexed addresses.
func reverseBytesCases() []Case {
	var cases []Case
	for _, w := range []uint8{16, 32, 64} {
		cases = append(cases, Case{Name: "integer/reverse-bytes-" + itoa(w), Inputs: unary(w),
			Build: straight(append(params(w), ir.Instruction{Op: ir.OpReverseBytes, Bits: w, A: 0})...)})
		// The result feeds arithmetic, so a target must leave it zero-extended.
		cases = append(cases, Case{Name: "integer/reverse-bytes-" + itoa(w) + "-add", Inputs: binary(w),
			Build: straight(append(params(w, w), ir.Instruction{Op: ir.OpReverseBytes, Bits: w, A: 0},
				ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: 2}, ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: 1},
				ir.Instruction{Op: ir.OpAdd, Bits: 64, A: 3, B: 4})...)})
	}
	cases = append(cases, Case{Name: "integer/reverse-bytes-128", Inputs: binary(64), Build: built(func(b *nyanjit.Builder) {
		x := b.ReverseBytes(b.Concat(b.Param(), b.Param()))
		b.Return(b.Xor(b.ExtractBits(x, 0, 64), b.RotateLeft(b.ExtractBits(x, 64, 64), 1)))
	})})
	for _, offset := range []uint64{0, 1, 8, 13} {
		cases = append(cases, Case{Name: name("memory", ir.OpLoad64, 64, ir.ByteOrderBigEndian, offset), Buffer: memorySize, Inputs: [][]uint64{{}},
			Build: straight(append(params(nativeBits),
				ir.Instruction{Op: ir.OpConst, Bits: nativeBits, Imm: offset},
				ir.Instruction{Op: ir.OpAdd, Bits: nativeBits, A: 0, B: 1},
				ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 2, ByteOrder: ir.ByteOrderBigEndian})...)})
		cases = append(cases, Case{Name: name("memory", ir.OpStore64, 64, ir.ByteOrderBigEndian, offset), Buffer: memorySize,
			Inputs: [][]uint64{{0x0123456789abcdef}, {^uint64(0)}, {0}},
			Build: straight(append(params(nativeBits, 64),
				ir.Instruction{Op: ir.OpConst, Bits: nativeBits, Imm: offset},
				ir.Instruction{Op: ir.OpAdd, Bits: nativeBits, A: 0, B: 2},
				ir.Instruction{Op: ir.OpStore64, A: 3, B: 1, ByteOrder: ir.ByteOrderBigEndian},
				ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 0})...)})
	}
	// Indexed addresses: buffer + index*8.
	index := [][]uint64{{0}, {1}, {3}}
	cases = append(cases, Case{Name: "memory/OpLoad64-64-BigEndian-indexed", Buffer: memorySize, Inputs: index,
		Build: straight(append(params(nativeBits, nativeBits),
			ir.Instruction{Op: ir.OpAddScaled, Bits: nativeBits, A: 0, B: 1, Imm: 8},
			ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 2, ByteOrder: ir.ByteOrderBigEndian})...)})
	cases = append(cases, Case{Name: "memory/OpStore64-64-BigEndian-indexed", Buffer: memorySize, Inputs: index,
		Build: straight(append(params(nativeBits, nativeBits),
			ir.Instruction{Op: ir.OpAddScaled, Bits: nativeBits, A: 0, B: 1, Imm: 8},
			ir.Instruction{Op: ir.OpConst, Imm: 0x8899aabbccddeeff},
			ir.Instruction{Op: ir.OpStore64, A: 2, B: 3, ByteOrder: ir.ByteOrderBigEndian},
			ir.Instruction{Op: ir.OpLoad64, Bits: 64, A: 0})...)})
	return cases
}
