package machine

import (
	"github.com/moriyoshi/nyanjit/ir"
	"testing"
)

func TestHighProductPortableSelection(t *testing.T) {
	for _, registerBits := range []uint8{32, 64} {
		for _, width := range []uint8{8, 16, 32, 64} {
			ops := []ir.Instruction{{Op: ir.OpParam, Bits: width}, {Op: ir.OpParam, Bits: width}, {Op: ir.OpMulHigh, Bits: width, A: 0, B: 1}}
			target := incSelectionTarget{arch: "386", bits: registerBits}
			if registerBits == 64 {
				target.arch = "arm64"
			}
			selected, err := SelectProgram(ops, nil, 2, 2, nil, 2, target)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifySelectedProgram(ops, selected, 2, 2, registerBits, target.NativeByteOrder()); err != nil {
				t.Fatal(err)
			}
		}
	}
}
