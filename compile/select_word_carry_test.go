package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

type nativeWordCarryTarget struct{ incSelectionTarget }

func (nativeWordCarryTarget) CanWordCarry(width uint8) bool { return width == 64 }

func TestWordCarrySelectionEquivalenceAndFusion(t *testing.T) {
	for _, target := range []selectionTarget{incSelectionTarget{arch: "386", bits: 32}, incSelectionTarget{arch: "arm64", bits: 64}, nativeWordCarryTarget{incSelectionTarget{arch: "ppc64le", bits: 64}}} {
		for _, width := range []uint8{8, 16, 32, 64} {
			for _, subtract := range []bool{false, true} {
				carry, high := op.OpCarryAdd, op.OpAddWithCarry
				if subtract {
					carry, high = op.OpBorrowSub, op.OpSubWithBorrow
				}
				for _, shared := range []bool{false, true} {
					ops := []ir.Instruction{{Op: op.OpParam, Bits: width}, {Op: op.OpParam, Bits: width}, {Op: op.OpParam, Bits: width}, {Op: op.OpParam, Bits: width}, {Op: carry, Bits: width, A: 0, B: 1}, {Op: high, Bits: width, A: 2, B: 3, C: 4}}
					exports := []int(nil)
					if shared {
						exports = []int{4}
					}
					selected, err := testSelectProgram(ops, nil, 4, 5, exports, 4, target)
					if err != nil {
						t.Fatal(err)
					}
					if err := verifySelectedProgram(ops, selected, 5, 4, target.RegisterBits(), target.NativeByteOrder()); err != nil {
						t.Fatal(err)
					}
					if target.Architecture() == "ppc64le" && width == 64 {
						fused := false
						for _, ins := range selected.Instructions {
							if ins.Op == machine.SelectedWordCarry {
								fused = len(ins.Args) == 4
							}
						}
						if fused == shared {
							t.Fatalf("carry fusion shared=%v: %+v", shared, selected.Instructions)
						}
					}
				}
			}
		}
	}
}
