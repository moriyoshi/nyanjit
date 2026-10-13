package compile

import (
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestVectorCountWidths(t *testing.T) {
	for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "ppc64le", "loong64"} {
		word := newMachineTarget(mustTarget(t, TargetSpec{Architecture: arch}).options(Func{})).RegisterBits()
		policies := []VectorPolicy{VectorsAuto(), VectorsOff()}
		if arch == "loong64" {
			policies = append(policies, VectorsRequire("lsx"))
		}
		for _, lane := range []uint8{8, 16, 32, 64} {
			for _, bits := range []uint8{8, 16, 32, 64} {
				for _, op := range []ir.Opcode{ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy} {
					for _, vectors := range policies {
						t.Run(fmt.Sprintf("%s/lane%d/count%d/op%d/scalar%v", arch, lane, bits, op, vectors != VectorsAuto()), func(t *testing.T) {
							p := &ir.Program{Arity: 2, Result: 4, Instructions: []ir.Instruction{
								{Op: ir.OpParam, Bits: word}, {Op: ir.OpParam, Bits: bits},
								{Op: ir.OpVLoad, Type: ir.Vec(128/uint16(lane), lane), Bits: lane, A: 0},
								{Op: op, Type: ir.Vec(128/uint16(lane), lane), Bits: lane, A: 2, B: 1},
								{Op: ir.OpVExtract, Bits: lane, A: 3},
							}}
							_, err := lowerListing(p, TargetSpec{Architecture: arch, Vectors: vectors}, Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}})
							if err != nil {
								t.Fatal(err)
							}
						})
					}
				}
			}
		}
	}
}
