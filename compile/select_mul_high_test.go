package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

type nativeMulHighTarget struct{ incSelectionTarget }

func (nativeMulHighTarget) CanMulHigh(width uint8) bool { return width == 64 }

// Without a signed high-product instruction, selection forms one from
// narrower or unsigned products with the same result.
func TestMulHighSignedFallbackEquivalence(t *testing.T) {
	for _, target := range []selectionTarget{incSelectionTarget{arch: "386", bits: 32}, incSelectionTarget{arch: "arm64", bits: 64}, nativeMulHighTarget{incSelectionTarget{arch: "ppc64le", bits: 64}}} {
		for _, width := range []uint8{8, 16, 32, 64} {
			ops := []ir.Instruction{{Op: op.OpParam, Bits: width}, {Op: op.OpParam, Bits: width}, {Op: op.OpMulHighSigned, Bits: width, A: 0, B: 1}}
			selected, err := testSelectProgram(ops, nil, 2, 2, nil, 2, target)
			if err != nil {
				t.Fatal(err)
			}
			for _, ins := range selected.Instructions {
				if ins.Op == machine.SelectedMulHighSigned {
					t.Fatalf("%s: native signed high product without the capability", target.Architecture())
				}
			}
			if err := verifySelectedProgram(ops, selected, 2, 2, target.RegisterBits(), target.NativeByteOrder()); err != nil {
				t.Fatalf("%s %d bits: %v", target.Architecture(), width, err)
			}
		}
	}
}
