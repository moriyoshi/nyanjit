//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSIMDPublicZeroAndInsertZero(t *testing.T) {
	m, ptr := simdMemory(t)
	for _, bits := range []uint8{8, 16, 32, 64} {
		for _, total := range []uint16{128, 256} {
			for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
				typ := ir.Vec(total/uint16(bits), bits)
				lane := uint64(typ.Lanes - 1)
				value := uint64(0x8123456789abcdef) & (^uint64(0) >> (64 - bits))
				p := &ir.Program{Arity: 1, Result: 5, Instructions: []ir.Instruction{
					{Op: ir.OpParam, Bits: builder.NativeBits},
					{Op: ir.OpConst, Bits: bits, Imm: value},
					{Op: ir.OpVZero, Type: typ, Bits: bits},
					{Op: ir.OpVInsertZero, Type: typ, Bits: bits, A: 2, B: 1, Imm: lane},
					{Op: ir.OpVStore, Type: typ, Bits: bits, A: 0, B: 3},
					{Op: ir.OpVExtract, Bits: bits, A: 3, Imm: lane},
				}}
				c, err := n.Compile(p, compile.TargetSpec{Vectors: vectors}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
				if err != nil {
					t.Fatalf("lane%d total%d vectors %v: %v", bits, total, vectors, err)
				}
				for k := range m {
					m[k] = 0xa5
				}
				lo, hi, err := c.CallPair(ptr + 1)
				c.Close()
				if got := execution.Wide(lo, hi); err != nil || got != value {
					t.Fatalf("lane%d total%d got%x,%v want%x", bits, total, got, err, value)
				}
				if m[0] != 0xa5 || m[1+total/8] != 0xa5 {
					t.Fatal("zero/insert overwrote guard")
				}
				for k := 0; k < int(typ.Lanes); k++ {
					want := uint64(0)
					if uint64(k) == lane {
						want = value
					}
					if got := readSIMDLane(m[1+k*int(bits/8):], bits); got != want {
						t.Fatalf("lane%d got%x want%x", k, got, want)
					}
				}
			}
		}
	}
}

func TestSIMDInsertZeroOverwritesLane(t *testing.T) {
	mem, ptr := simdMemory(t)
	for _, bits := range []uint8{8, 16, 32, 64} {
		for _, total := range []uint16{128, 256} {
			typ := ir.Vec(total/uint16(bits), bits)
			for lane := uint64(0); lane < uint64(typ.Lanes); lane++ {
				for _, vectors := range []compile.VectorPolicy{compile.VectorsAuto(), compile.VectorsOff()} {
					mask := ^uint64(0) >> (64 - bits)
					first, second := uint64(0xa5a5a5a5a5a5a5a5)&mask, uint64(0x123456789abcdef0)&mask
					p := &ir.Program{Arity: 1, Result: 8, Instructions: []ir.Instruction{
						{Op: ir.OpParam, Bits: builder.NativeBits},
						{Op: ir.OpConst, Bits: bits, Imm: first},
						{Op: ir.OpConst, Bits: bits, Imm: second},
						{Op: ir.OpVSplat, Type: typ, Bits: bits, A: 1},
						{Op: ir.OpVInsertZero, Type: typ, Bits: bits, A: 3, B: 2, Imm: lane},
						{Op: ir.OpVInsertZero, Type: typ, Bits: bits, A: 4, B: 1, Imm: lane},
						{Op: ir.OpVInsertZero, Type: typ, Bits: bits, A: 5, B: 2, Imm: lane},
						{Op: ir.OpVStore, Type: typ, Bits: bits, A: 0, B: 6},
						{Op: ir.OpVExtract, Bits: bits, A: 6, Imm: lane},
					}}
					code, err := n.Compile(p, compile.TargetSpec{Vectors: vectors}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
					if err != nil {
						t.Fatal(err)
					}
					lo, hi, err := code.CallPair(ptr + 1)
					code.Close()
					if got := execution.Wide(lo, hi); err != nil || got != second {
						t.Fatalf("bits%d total%d lane%d vectors %v: %x,%v want%x", bits, total, lane, vectors, got, err, second)
					}
					for k := 0; k < int(typ.Lanes); k++ {
						want := first
						if uint64(k) == lane {
							want = second
						}
						if got := readSIMDLane(mem[1+k*int(bits/8):], bits); got != want {
							t.Fatalf("bits%d lane%d got%x want%x", bits, k, got, want)
						}
					}
				}
			}
		}
	}
}
