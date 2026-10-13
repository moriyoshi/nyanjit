package arm64

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func TestSizeAndBytePlansReuseStackStorage(t *testing.T) {
	cases := []machine.Instruction{
		{Op: machine.OpConst, Imm: 0x123456789abcdef0},
		{Op: machine.OpFloatFMA, Bits: 64, A: 1, B: 2, C: 3, D: 4},
		{Op: machine.OpFloatReload, A: backend.VectorRegister(3), Slot: 4095},
		{Op: machine.OpVector, Arithmetic: op.OpVMaskBits, Bits: 8, A: 0, B: backend.VectorRegister(1)},
	}
	for _, ins := range cases {
		if n := testing.AllocsPerRun(100, func() { PhysicalSize(ins, abi.StackPlatform, 9) }); n != 0 {
			t.Fatalf("op%d size query allocates %g", ins.Op, n)
		}
	}
	e := NewByteAssembler(abi.StackPlatform, 9)
	e.words = make([]uint32, 0, 32)
	if n := testing.AllocsPerRun(100, func() {
		e.words = e.words[:0]
		e.Constant(0, 0x123456789abcdef0)
		e.Float(cases[1])
		if err := e.FloatMemory(cases[2]); err != nil {
			panic(err)
		}
		if err := e.Vector(cases[3]); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("byte plans allocate %g with caller-owned output capacity", n)
	}
}
