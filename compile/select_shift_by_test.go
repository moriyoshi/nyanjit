package compile

import (
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

type nativeShiftSelectionTarget struct{ incSelectionTarget }

func (nativeShiftSelectionTarget) CanShiftBy(uint8) bool { return true }

func TestVariableShiftSelectionRetainsNativeOperationOrExpandsPortably(t *testing.T) {
	for _, target := range []selectionTarget{
		incSelectionTarget{arch: "arm64", bits: 64},
		incSelectionTarget{arch: "386", bits: 32},
		incSelectionTarget{arch: "amd64", bits: 64},
		incSelectionTarget{arch: "riscv64", bits: 64},
		nativeShiftSelectionTarget{incSelectionTarget{arch: "ppc64le", bits: 64}},
	} {
		for _, width := range []uint8{8, 16, 32, 64} {
			for _, countWidth := range []uint8{8, 16, 32, 64} {
				for _, operation := range []op.Opcode{op.OpShiftLeftBy, op.OpShiftRightBy, op.OpShiftRightArithmeticBy, op.OpRotateLeftBy, op.OpRotateRightBy} {
					t.Run(fmt.Sprintf("%s/%d/count%d/op%d", target.Architecture(), width, countWidth, operation), func(t *testing.T) {
						ops := []ir.Instruction{{Op: op.OpParam, Bits: width}, {Op: op.OpParam, Bits: countWidth}, {Op: operation, Bits: width, A: 0, B: 1}}
						selected, err := testSelectProgram(ops, nil, 2, 2, nil, 2, target)
						if err != nil {
							t.Fatal(err)
						}
						found := false
						for _, ins := range selected.Instructions {
							found = found || ins.Op == machine.SelectedShiftBy
						}
						if found != (target.Architecture() == "ppc64le") {
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
