package compile

import (
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

// Model the write addresses, avoiding a real mapping at the 4 GiB boundary.
// Offset arithmetic must use the target pointer width even when the source
// address is a 32-bit scalar on a 64-bit target.
func TestNarrowMemoryFallbackAddressBoundary(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64", "386", "riscv64", "loong64", "ppc64le"} {
		for _, bits := range []uint8{16, 32} {
			for _, order := range []ir.ByteOrder{ir.ByteOrderLittleEndian, ir.ByteOrderBigEndian} {
				t.Run(fmt.Sprintf("%s/%d/%d", arch, bits, order), func(t *testing.T) {
					op := ir.OpStore16
					if bits == 32 {
						op = ir.OpStore32
					}
					const address = uint64(0xffffffff)
					const data = uint64(0xfeedbeef89abcdef)
					p := &ir.Program{Result: 1, Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 32, Imm: address}, {Op: ir.OpConst, Bits: 64, Imm: data}, {Op: op, Bits: bits, A: 0, B: 1, ByteOrder: order}}}
					q, _, err := legalizeVectorsWithMemoryFallback(p, mustTarget(t, TargetSpec{Architecture: arch}).options(Func{}), true)
					if err != nil {
						t.Fatal(err)
					}
					values := make([]uint64, len(q.Instructions))
					writes := map[uint64]byte{}
					for id, ins := range q.Instructions {
						var v uint64
						switch ins.Op {
						case ir.OpConst:
							v = ins.Imm
						case ir.OpCastUnsigned:
							v = values[ins.A]
						case ir.OpAdd:
							v = values[ins.A] + values[ins.B]
						case ir.OpShiftRight:
							v = values[ins.A] >> ins.Imm
						case ir.OpStore8:
							writes[values[ins.A]] = byte(values[ins.B])
							continue
						default:
							t.Fatalf("unexpected scalar operation %s", ins.Op)
						}
						width := ins.ValueType().LaneBits
						if width < 64 {
							v &= (uint64(1) << width) - 1
						}
						values[id] = v
					}
					if len(writes) != int(bits/8) {
						t.Fatalf("write footprint: %v", writes)
					}
					for offset := 0; offset < int(bits/8); offset++ {
						at := address + uint64(offset)
						if arch == "386" {
							at = uint64(uint32(at))
						}
						shift := offset * 8
						if order == ir.ByteOrderBigEndian {
							shift = int(bits) - 8 - shift
						}
						if got, ok := writes[at]; !ok || got != byte(data>>shift) {
							t.Fatalf("byte%d at%x: got%x present%v, writes%v", offset, at, got, ok, writes)
						}
					}
				})
			}
		}
	}
}
