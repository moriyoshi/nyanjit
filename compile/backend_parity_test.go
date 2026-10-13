package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestBackendCapabilityMatrix(t *testing.T) {
	for _, arch := range []string{"amd64", "386", "arm64", "riscv64", "ppc64le", "loong64"} {
		target := newMachineTarget(mustTarget(t, TargetSpec{Architecture: arch}).options(Func{}))
		shift := target.(interface{ CanShiftBy(uint8) bool })
		mul := target.(interface{ CanMulHigh(uint8) bool })
		signed := target.(interface{ CanMulHighSigned(uint8) bool })
		carry := target.(interface{ CanWordCarry(uint8) bool })
		for _, w := range []uint8{8, 16, 32, 64} {
			if !shift.CanShiftBy(w) {
				t.Errorf("%s shift%d lost native selection", arch, w)
			}
			wantMul := arch != "386" || w == 32
			if mul.CanMulHigh(w) != wantMul {
				t.Errorf("%s mulhigh%d", arch, w)
			}
			if signed.CanMulHighSigned(w) != wantMul {
				t.Errorf("%s signed mulhigh%d", arch, w)
			}
			wantCarry := w == 64 && arch != "386" || w == 32 && (arch == "386" || arch == "arm64")
			if carry.CanWordCarry(w) != wantCarry {
				t.Errorf("%s carry%d", arch, w)
			}
		}
		for _, probe := range []struct {
			ir       ir.Opcode
			selected machine.SelectedOpcode
		}{
			{ir.OpShiftRightBy, machine.SelectedShiftBy}, {ir.OpRotateLeftBy, machine.SelectedShiftBy},
			{ir.OpMulHigh, machine.SelectedMulHigh}, {ir.OpCarryAdd, machine.SelectedCarryBit},
		} {
			w := uint8(64)
			if arch == "386" {
				w = 32
			}
			ops := []ir.Instruction{{Op: ir.OpParam, Bits: w}, {Op: ir.OpParam, Bits: w}, {Op: probe.ir, Bits: w, A: 0, B: 1}}
			selected, err := testSelectProgram(ops, nil, 2, 2, nil, abi.MaxArguments(), target)
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, ins := range selected.Instructions {
				if ins.Op == probe.selected {
					found++
				}
				if ins.Op == machine.SelectedSelect {
					t.Fatalf("%s op%d expanded to a software Select: %+v", arch, probe.ir, selected.Instructions)
				}
			}
			if found != 1 {
				t.Fatalf("%s op%d: expected one native selected operation, got%d: %+v", arch, probe.ir, found, selected.Instructions)
			}
		}
	}
}
