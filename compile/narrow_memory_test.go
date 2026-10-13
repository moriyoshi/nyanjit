package compile_test

import (
	"fmt"
	"strings"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestNarrowMemoryDataListing(t *testing.T) {
	type record struct {
		A uint32
		B uint16
	}
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "ppc64le", "loong64"} {
		for _, field := range []string{"A", "B"} {
			t.Run(arch+field, func(t *testing.T) {
				b := n.NewBuilder()
				d := n.ValueParam[record](b)
				b.Return(d.Field(field))
				p, e := b.Finish()
				if e != nil {
					t.Fatal(e)
				}
				bits := uint8(32)
				if field == "B" {
					bits = 16
				}
				loads := 0
				for _, i := range p.Clone().Instructions {
					if i.NarrowMemoryBits() != 0 {
						loads++
						if !i.Aligned || i.Bits != bits {
							t.Fatalf("incorrect memory op %+v", i)
						}
					}
					if i.Op == ir.OpLoad8 {
						t.Fatal("byte assembly remains")
					}
				}
				if loads != 1 {
					t.Fatalf("got %d loads", loads)
				}
				s, e := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
				if e != nil {
					t.Fatal(e)
				}
				mnemonic := map[string][2]string{"amd64": {"dword ptr [", "word ptr ["}, "386": {"dword ptr [", "word ptr ["}, "arm64": {"ldr W", "ldrh W"}, "riscv64": {"lwu ", "lhu "}, "ppc64le": {"lwz ", "lhz "}, "loong64": {"ld.wu ", "ld.hu "}}[arch]
				index := 0
				if bits == 16 {
					index = 1
				}
				if !strings.Contains(s, mnemonic[index]) {
					t.Fatalf("missing native load:\n%s", s)
				}
				for _, byteLoad := range []string{"byte ptr [", "ldrb ", "lbu ", "lbz ", "ld.bu "} {
					if strings.Contains(s, byteLoad) {
						t.Fatalf("byte load remains:\n%s", s)
					}
				}
			})
		}
	}
}

// Public addresses can be shared, indexed and far outside immediate ranges.
// The selector materializes them in a GP register for narrow operations.
func TestNarrowMemoryAddressAndEndianListing(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "ppc64le", "loong64"} {
		for _, bits := range []uint8{16, 32} {
			for _, aligned := range []bool{false, true} {
				for _, order := range []ir.ByteOrder{ir.ByteOrderNative, ir.ByteOrderLittleEndian, ir.ByteOrderBigEndian} {
					t.Run(fmt.Sprintf("%s/%d/%v/%d", arch, bits, aligned, order), func(t *testing.T) {
						for _, disp := range []uint64{0, 2047, 2048, 32767, 32768, ^uint64(0) - 1} {
							load, store := ir.OpLoad16, ir.OpStore16
							if bits == 32 {
								load, store = ir.OpLoad32, ir.OpStore32
							}
							p := &ir.Program{Arity: 2, Result: 6, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 64}, {Op: ir.OpParam, Bits: 64}, {Op: ir.OpConst, Bits: 64, Imm: disp}, {Op: ir.OpAdd, Bits: 64, A: 0, B: 2}, {Op: store, Bits: bits, A: 3, B: 1, Aligned: aligned, ByteOrder: order}, {Op: load, Bits: bits, A: 3, Aligned: aligned, ByteOrder: order}, {Op: ir.OpCastUnsigned, Bits: 64, A: 5}}}
							s, e := lowerListing(p, compile.TargetSpec{Architecture: arch}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
							if e != nil {
								t.Fatalf("disp%x: %v", disp, e)
							}
							if s == "" {
								t.Fatal("empty listing")
							}
						}
					})
				}
			}
		}
	}
}
