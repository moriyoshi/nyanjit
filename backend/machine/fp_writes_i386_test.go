package machine_test

import (
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

func Test386FloatWrites(t *testing.T) {
	xmm := backend.VectorRegister
	for _, tt := range []struct {
		name string
		ins  machine.Instruction
		want []backend.Register
	}{
		{"pack32", machine.Instruction{Op: machine.OpFloatMove, Bits: 32, A: xmm(0), B: 0}, []backend.Register{xmm(0)}},
		{"pack64", machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: xmm(1), B: 0, HighB: 2}, []backend.Register{xmm(1), xmm(2)}},
		{"unpack32", machine.Instruction{Op: machine.OpFloatMove, Bits: 32, A: 0, B: xmm(1)}, nil},
		{"unpack64-preserves-source", machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: 0, HighA: 2, B: xmm(1)}, []backend.Register{xmm(2)}},
		{"reload32", machine.Instruction{Op: machine.OpFloatReload, Bits: 32, A: xmm(7), Slot: 1}, []backend.Register{xmm(7)}},
		{"reload64", machine.Instruction{Op: machine.OpFloatReload, Bits: 64, A: xmm(7), Slot: 1}, []backend.Register{xmm(7)}},
		{"spill64", machine.Instruction{Op: machine.OpFloatSpill, Bits: 64, A: xmm(7), Slot: 1}, nil},
		{"resident-arithmetic", machine.Instruction{Op: machine.OpFloatBinary, Bits: 64, A: xmm(7), B: xmm(3), C: xmm(4), Arithmetic: op.OpFAdd}, []backend.Register{xmm(7)}},
		{"copy", machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: xmm(3), B: xmm(4)}, []backend.Register{xmm(3)}},
		{"self-copy", machine.Instruction{Op: machine.OpFloatMove, Bits: 64, A: xmm(3), B: xmm(3)}, nil},
		{"arithmetic-xmm", machine.Instruction{Op: machine.OpFloatBinary, Bits: 64, A: xmm(0), B: xmm(0), C: xmm(1), Arithmetic: op.OpFAdd}, []backend.Register{xmm(0)}},
		{"arithmetic-pairs", machine.Instruction{Op: machine.OpFloatBinary, Bits: 64, A: 0, HighA: 2, B: 0, HighB: 2, C: 3, HighC: 1, Arithmetic: op.OpFMul}, []backend.Register{xmm(0), xmm(1), xmm(2)}},
		{"convert-source64", machine.Instruction{Op: machine.OpFloatUnary, Bits: 32, A: xmm(0), B: 0, HighB: 2, Arithmetic: op.OpFConvert}, []backend.Register{xmm(0), xmm(2)}},
		{"convert-source32", machine.Instruction{Op: machine.OpFloatUnary, Bits: 64, A: xmm(0), B: 0, Arithmetic: op.OpFConvert}, []backend.Register{xmm(0)}},
		{"mask", machine.Instruction{Op: machine.OpFloatBits, Bits: 64, A: xmm(3), B: xmm(4), Arithmetic: op.OpAnd}, []backend.Register{xmm(3)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := machine.FloatWrites(tt.ins, "386")
			slices.Sort(got)
			slices.Sort(tt.want)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("writes=%v want %v", got, tt.want)
			}
		})
	}
}
