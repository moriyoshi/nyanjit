package main

import (
	"encoding/binary"
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

var roundConstants = [...]uint32{
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5,
	0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
	0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
	0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
	0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
	0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
	0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5,
	0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
	0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}

func compressionProgram() (*ir.SealedProgram, error) {
	b := nyanjit.NewBuilder()
	state, schedule := b.ParamNative(), b.ParamNative()
	shape := ir.Vec(4, 32)
	address := func(base nyanjit.Value, word int) nyanjit.Value {
		return b.Add(base, b.ConstNative(uintptr(word*16)))
	}
	loadWord := func(word int) nyanjit.Value { return b.VLoad(shape, address(schedule, word&15)) }
	constant := func(word uint32) nyanjit.Value {
		var data [16]byte
		for lane := 0; lane < 4; lane++ {
			binary.LittleEndian.PutUint32(data[lane*4:], word)
		}
		return b.ConstVector(shape, data[:])
	}
	sigma := func(x nyanjit.Value, a, c, shift uint8) nyanjit.Value {
		return b.Xor(b.Xor(b.RotateRight(x, a), b.RotateRight(x, c)), b.ShiftRight(x, shift))
	}
	bigSigma := func(x nyanjit.Value, a, c, d uint8) nyanjit.Value {
		return b.Xor(b.Xor(b.RotateRight(x, a), b.RotateRight(x, c)), b.RotateRight(x, d))
	}
	var working [8]nyanjit.Value
	for i := range working {
		working[i] = b.VLoad(shape, address(state, i))
	}
	// Unroll at construction time: phase 1 scalarizes programs with labels or
	// mutable Vars, so a generated round loop would lose native SIMD residency.
	for round, k := range roundConstants {
		b.Enter()
		w := loadWord(round)
		if round >= 16 {
			s0 := sigma(loadWord(round-15), 7, 18, 3)
			s1 := sigma(loadWord(round-2), 17, 19, 10)
			w = b.Add(b.Add(w, s0), b.Add(loadWord(round-7), s1))
			// The 16-word ring bounds live schedule storage. Read operands before
			// replacing W[t-16]; this store materializes the shared expression.
			b.VStore(address(schedule, round&15), w)
		}
		a, bb, c, d, e, f, g, h := working[0], working[1], working[2], working[3], working[4], working[5], working[6], working[7]
		// Ch(e,f,g) = g XOR (e AND (f XOR g)); Maj(a,b,c) =
		// (a AND b) XOR (c AND (a XOR b)). Neither needs vector NOT.
		choose := b.Xor(g, b.And(e, b.Xor(f, g)))
		majority := b.Xor(b.And(a, bb), b.And(c, b.Xor(a, bb)))
		t1 := b.Add(b.Add(h, bigSigma(e, 6, 11, 25)), b.Add(b.Add(choose, constant(k)), w))
		t2 := b.Add(bigSigma(a, 2, 13, 22), majority)
		working = [8]nyanjit.Value{b.Add(t1, t2), a, bb, c, b.Add(d, t1), e, f, g}
		// Materialize this round now; otherwise deferred state trees would
		// retain all 64 schedule words until the final stores. Only the two
		// newly computed state words need promotion to the outer scope.
		b.Leave(working[0], working[4])
	}
	for i, word := range working {
		p := address(state, i)
		b.VStore(p, b.Add(b.VLoad(shape, p), word))
	}
	b.Return(b.ConstNative(0))
	return b.Finish()
}
