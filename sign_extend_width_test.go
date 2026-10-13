package nyanjit_test

import (
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

// OpSignExtend extends the low Imm bits to Bits; a result narrower than 64
// bits is zero-extended above Bits like every other narrow value.
func TestSignExtendToNarrowWidth(t *testing.T) {
	for _, bits := range []uint8{16, 32, 64} {
		for _, from := range []uint8{8, 16, 32} {
			if from > bits {
				continue
			}
			pb := ir.NewProgramBuilder()
			x := pb.Param(pb.Entry(), ir.Scalar(64))
			v := pb.Append(pb.Entry(), ir.Instruction{Op: ir.OpSignExtend, Bits: bits, A: x, Imm: uint64(from)})
			// Widening to 64 bits must not reveal sign bits above Bits.
			wide := pb.Append(pb.Entry(), ir.Instruction{Op: ir.OpCastUnsigned, Bits: 64, A: v})
			pb.Return(pb.Entry(), wide)
			program, err := pb.Seal()
			if err != nil {
				t.Fatal(err)
			}
			for _, arch := range []string{"amd64", "arm64", "386", "riscv64", "loong64", "ppc64le"} {
				target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
				if err != nil {
					t.Fatal(err)
				}
				unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
				if err == nil {
					_, err = unit.Listing()
				}
				if err != nil {
					t.Errorf("Bits=%d Imm=%d on %s: %v", bits, from, arch, err)
				}
			}
			if runtime.GOOS != "linux" {
				continue
			}
			code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
			if err != nil {
				t.Fatal(err)
			}
			for _, in := range []uint64{0, 0x7f, 0x80, 0x8000, 0x80000000, 0x8080808080808080, 0x7f7f7f7f7f7f7f7f} {
				shift := 64 - from
				want := uint64(int64(in<<shift) >> shift)
				if bits < 64 {
					want &= 1<<bits - 1
				}
				lo, hi, err := code.CallPair(execution.Words(in)...)
				if got := execution.Wide(lo, hi); err != nil || got != want {
					t.Errorf("Bits=%d Imm=%d f(%#x) = %#x, %v; want %#x", bits, from, in, got, err, want)
				}
			}
			code.Close()
		}
	}
}
