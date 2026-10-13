package arm64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestFloatSizeMatchesExpansionAcrossRegisterBanks(t *testing.T) {
	cases := []struct {
		op         machine.Opcode
		arithmetic op.Opcode
	}{
		{machine.OpFloatMove, 0}, {machine.OpFloatBits, op.OpAnd},
		{machine.OpFloatBits, op.OpXor}, {machine.OpFloatUnary, op.OpFSqrt},
		{machine.OpFloatUnary, op.OpFConvert}, {machine.OpFloatFMA, 0},
		{machine.OpFloatBinary, op.OpFAdd}, {machine.OpFloatBinary, op.OpFSub},
		{machine.OpFloatBinary, op.OpFMul}, {machine.OpFloatBinary, op.OpFDiv},
	}
	for _, c := range cases {
		for _, bits := range []uint8{32, 64} {
			for banks := 0; banks < 16; banks++ {
				regs := [4]backend.Register{1, 2, 3, 4}
				for n := range regs {
					if banks&(1<<n) != 0 {
						regs[n] = backend.VectorRegister(uint8(regs[n]))
					}
				}
				i := machine.Instruction{Op: c.op, Arithmetic: c.arithmetic, Bits: bits, A: regs[0], B: regs[1], C: regs[2], D: regs[3]}
				if got, want := PhysicalSize(i, abi.StackNoUse, 0), floatPlan(i, false).n*4; got != want {
					t.Fatalf("%+v: size %d, expansion %d", i, got, want)
				}
			}
		}
	}
	for _, bits := range []uint8{32, 64} {
		i := machine.Instruction{Op: machine.OpFloatMove, Bits: bits, A: backend.VectorRegister(1), B: backend.VectorRegister(1)}
		if got := PhysicalSize(i, abi.StackNoUse, 0); got != 0 {
			t.Fatalf("self move = %d", got)
		}
	}
}

func TestPhysicalSizeRetainsSpecializedValidation(t *testing.T) {
	firstInvalid := machine.Opcode(0)
	for firstInvalid.Valid() {
		firstInvalid++
	}
	for _, i := range []machine.Instruction{
		{Op: machine.OpVector, Arithmetic: op.OpAdd, Bits: 32, A: backend.VectorRegister(0), B: backend.VectorRegister(1), C: backend.VectorRegister(2), ByteOrder: 255},
		{Op: machine.OpVectorMove, A: backend.VectorRegister(0), B: 32},
		{Op: machine.OpVectorSpill, A: backend.VectorRegister(0), Slot: -1},
		{Op: machine.OpFloatBinary, Arithmetic: op.OpAdd, Bits: 32},
		{Op: machine.OpFloatFMA, Bits: 32, A: 32},
		{Op: machine.OpFloatMove, Bits: 16, A: backend.VectorRegister(0)},
		{Op: machine.OpFloatReload, Bits: 32, A: backend.VectorRegister(0), Slot: -1},
		{Op: machine.OpFloatReload, Bits: 32, A: backend.VectorRegister(0), ByteOrder: 255},
		{Op: machine.OpLiteralLoad, Bits: 128, A: 31},
		{Op: machine.OpLiteralLoad, Bits: 128, A: backend.VectorRegister(0), ByteOrder: 255},
		{Op: firstInvalid},
		{Op: 255},
	} {
		if got := PhysicalSize(i, abi.StackNoUse, 0); got != 0 {
			t.Fatalf("malformed %+v has size %d", i, got)
		}
	}
}
