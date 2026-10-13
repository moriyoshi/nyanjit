package compile

import (
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// The allocator frees FixedClobbers around the instruction and variable
// homes avoid ImplicitWrites; both must name the registers the division and
// conversion expansions write besides A.
func TestAMD64DivisionAndConversionClobbers(t *testing.T) {
	target := &amd64Target{}
	check := func(selected machineInstruction, physical machine.Instruction, want []physReg) {
		t.Helper()
		if got := target.FixedClobbers(selected); !slices.Equal(got, want) {
			t.Errorf("%+v: FixedClobbers %v, want %v", selected, got, want)
		}
		if got := target.ImplicitWrites(physical); !slices.Equal(got, want) {
			t.Errorf("%+v: ImplicitWrites %v, want %v", physical, got, want)
		}
	}
	for _, bits := range []uint8{8, 16, 32, 64} {
		for _, arithmetic := range []ir.Opcode{ir.OpDiv, ir.OpDivSigned, ir.OpRem, ir.OpRemSigned} {
			want := []physReg{0, 2}
			if (arithmetic == ir.OpDivSigned || arithmetic == ir.OpRemSigned) && bits < 32 {
				want = append(want, 11)
			}
			check(machineInstruction{Op: mDivide, Arithmetic: arithmetic, Bits: bits},
				machine.Instruction{Op: machine.OpDivide, Arithmetic: arithmetic, Bits: bits}, want)
			for position := range 2 {
				if !target.CanReuse(machineInstruction{Op: mDivide, Arithmetic: arithmetic, Bits: bits}, position) {
					t.Errorf("%v/%d: divide input %d not reusable", arithmetic, bits, position)
				}
			}
		}
	}
	for _, bits := range []uint8{32, 64} {
		for _, source := range []uint64{32, 64} {
			for _, arithmetic := range []ir.Opcode{ir.OpFFromSigned, ir.OpFFromUnsigned} {
				var want []physReg
				if arithmetic == ir.OpFFromUnsigned && source == 64 {
					want = []physReg{11}
				}
				check(machineInstruction{Op: mIntToFloat, Arithmetic: arithmetic, Bits: bits, Imm: source},
					machine.Instruction{Op: machine.OpIntToFloat, Arithmetic: arithmetic, Bits: bits, Imm: source}, want)
			}
			for _, arithmetic := range []ir.Opcode{ir.OpFToSigned, ir.OpFToUnsigned} {
				check(machineInstruction{Op: mFloatToInt, Arithmetic: arithmetic, Bits: bits, Disp: source},
					machine.Instruction{Op: machine.OpFloatToInt, Arithmetic: arithmetic, Bits: bits, Disp: source}, nil)
			}
		}
	}
}
