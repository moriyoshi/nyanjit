package machine

import (
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

type nativeShiftSelectionTarget struct{ incSelectionTarget }

func (nativeShiftSelectionTarget) CanShiftBy(uint8) bool { return true }

func TestVariableShiftSelectionRetainsNativeOperationOrExpandsPortably(t *testing.T) {
	for _, target := range []SelectionTarget{
		incSelectionTarget{arch: "arm64", bits: 64},
		incSelectionTarget{arch: "386", bits: 32},
		incSelectionTarget{arch: "amd64", bits: 64},
		incSelectionTarget{arch: "riscv64", bits: 64},
		nativeShiftSelectionTarget{incSelectionTarget{arch: "ppc64", bits: 64}},
	} {
		for _, width := range []uint8{8, 16, 32, 64} {
			for _, countWidth := range []uint8{8, 16, 32, 64} {
				for _, operation := range []ir.Opcode{ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy} {
					t.Run(fmt.Sprintf("%s/%d/count%d/op%d", target.Architecture(), width, countWidth, operation), func(t *testing.T) {
						ops := []ir.Instruction{{Op: ir.OpParam, Bits: width}, {Op: ir.OpParam, Bits: countWidth}, {Op: operation, Bits: width, A: 0, B: 1}}
						selected, err := SelectProgram(ops, nil, 2, 2, nil, 2, target)
						if err != nil {
							t.Fatal(err)
						}
						found := false
						for _, ins := range selected.Instructions {
							found = found || ins.Op == SelectedShiftBy
						}
						if found != (target.Architecture() == "ppc64") {
							t.Fatal("native selection disagrees with target capability")
						}
						if err := verifySelectedProgram(ops, selected, 2, 2, target.RegisterBits(), target.NativeByteOrder()); err != nil {
							t.Fatal(err)
						}
					})
				}
			}
		}
	}
}
