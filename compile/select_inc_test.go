package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
	"github.com/moriyoshi/nyanjit/ir"
)

type incSelectionTarget struct {
	arch string
	bits uint8
}

func (target incSelectionTarget) Architecture() string        { return target.arch }
func (target incSelectionTarget) RegisterBits() uint8         { return target.bits }
func (incSelectionTarget) NativeByteOrder() op.ByteOrder      { return op.ByteOrderLittleEndian }
func (incSelectionTarget) CanScaled(uint64, uint64) bool      { return false }
func (incSelectionTarget) CanCombineScaled() bool             { return false }
func (incSelectionTarget) CanMulAdd() bool                    { return false }
func (incSelectionTarget) CanFoldAddress(uint64, uint64) bool { return false }
func (incSelectionTarget) CanFoldBaseOffset(uint64) bool      { return false }

func TestIncSelectsTheOtherOperandWhenBothAreConstants(t *testing.T) {
	for _, target := range []incSelectionTarget{{arch: "amd64", bits: 64}, {arch: "386", bits: 32}} {
		for _, operands := range [][2]int{{0, 1}, {1, 0}} {
			minusOne := verifyIntegerMask(target.bits)
			ops := []ir.Instruction{
				{Op: op.OpConst, Bits: target.bits, Imm: minusOne},
				{Op: op.OpConst, Bits: target.bits, Imm: 1},
				{Op: op.OpAdd, Bits: target.bits, A: operands[0], B: operands[1]},
			}
			selected, err := testSelectProgram(ops, nil, 0, 2, nil, 0, target)
			if err != nil {
				t.Fatalf("%s operands %v: %v", target.arch, operands, err)
			}
			last := selected.Instructions[len(selected.Instructions)-1]
			if last.Op != machine.SelectedInc || last.A != 0 {
				t.Errorf("%s operands %v: selected %v with source %d, want Inc of value 0", target.arch, operands, last.Op, last.A)
			}
		}
	}
}
