package arm64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Scalar sizing and the independent public-plan validator must agree even for
// unused GP operands. This protects the fast scalar dispatch's audit checks.
func TestScalarSizeValidationMatchesPlanEntry(t *testing.T) {
	for operation := machine.Opcode(0); operation.Valid(); operation++ {
		switch operation {
		case machine.OpVector, machine.OpVectorMove, machine.OpVectorSpill, machine.OpVectorReload,
			machine.OpFloatBinary, machine.OpFloatUnary, machine.OpFloatFMA, machine.OpFloatMove, machine.OpFloatBits,
			machine.OpFloatSpill, machine.OpFloatReload, machine.OpLiteralLoad:
			continue
		}
		base := machine.Instruction{Op: operation, Bits: 64, Arithmetic: op.OpAdd, A: 1, B: 2, C: 3, D: 4, E: 5, Imm: 4, Slot: 2, Data: make([]byte, 16)}
		check := func(i machine.Instruction) {
			t.Helper()
			if got, want := PhysicalSize(i, abi.StackNoUse, 0) > 0, validSizeFields(&i); got != want {
				t.Fatalf("%+v: nonzero size=%v, plan entry validity=%v", i, got, want)
			}
		}
		check(base)
		for n := 0; n < 256; n++ {
			i := base
			i.Arithmetic = op.Opcode(n)
			check(i)
			i = base
			i.ByteOrder = op.ByteOrder(n)
			check(i)
		}
		for _, bits := range []uint8{0, 1, 8, 16, 32, 64, 128, 255} {
			i := base
			i.Bits = bits
			check(i)
		}
		for _, imm := range []uint64{0, 1, 31, 32, 63, 64, 65, 1 << 20, 1<<20 + 1, ^uint64(0)} {
			i := base
			i.Imm = imm
			check(i)
		}
		for _, slot := range []int{-1, 0, 1, 4095, int(^uint(0) >> 1)} {
			i := base
			i.Slot = slot
			check(i)
		}
		for n := 0; n < 16; n++ {
			i := base
			i.Condition = op.BranchCondition(n)
			check(i)
			i = base
			i.CarryMode = machine.CarryMode(n)
			check(i)
		}
		for _, r := range []backend.Register{32, 63, 64, 95, 96, 255} {
			for field := 0; field < 5; field++ {
				i := base
				switch field {
				case 0:
					i.A = r
				case 1:
					i.B = r
				case 2:
					i.C = r
				case 3:
					i.D = r
				case 4:
					i.E = r
				}
				check(i)
			}
		}
	}
}
