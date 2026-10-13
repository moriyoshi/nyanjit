package irinterp_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

// laneProgram builds x = {0x80, 0x81, ...} and y = {0x10, 0x11, ...} of
// type t, applies op to them, and returns lane lane of the result.
func laneProgram(t ir.Type, result ir.Type, op ir.Instruction, lane uint64) *ir.Program {
	x, y := make([]byte, t.Bits()/8), make([]byte, t.Bits()/8)
	for i := range x {
		x[i], y[i] = byte(0x80+i), byte(0x10+i)
	}
	op.A, op.B = 0, 1
	if op.Op != ir.OpVShuffle {
		op.B = 0
	}
	return &ir.Program{Result: 3, Instructions: []ir.Instruction{
		{Op: ir.OpVConst, Type: t, Bits: t.LaneBits, Data: x},
		{Op: ir.OpVConst, Type: t, Bits: t.LaneBits, Data: y},
		op,
		{Op: ir.OpVExtract, Type: ir.Scalar(result.LaneBits), Bits: result.LaneBits, A: 2, Imm: lane},
	}}
}

func TestShuffleAndExtendLanes(t *testing.T) {
	for _, test := range []struct {
		name string
		t, r ir.Type
		op   ir.Instruction
		lane uint64
		want uint64
	}{
		{"shuffle-x", ir.Vec(4, 16), ir.Vec(4, 16), ir.Instruction{Op: ir.OpVShuffle, Data: []byte{3, 6, 1, 0}}, 0, 0x8786},
		{"shuffle-y", ir.Vec(4, 16), ir.Vec(4, 16), ir.Instruction{Op: ir.OpVShuffle, Data: []byte{3, 6, 1, 0}}, 1, 0x1514},
		{"broadcast", ir.Vec(16, 8), ir.Vec(16, 8), ir.Instruction{Op: ir.OpVShuffle, Data: []byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}}, 13, 0x89},
		{"widen-unsigned", ir.Vec(4, 16), ir.Vec(4, 32), ir.Instruction{Op: ir.OpVExtendUnsigned}, 2, 0x8584},
		{"widen-signed", ir.Vec(4, 16), ir.Vec(4, 32), ir.Instruction{Op: ir.OpVExtendSigned}, 2, 0xffff8584},
		{"widen-high-half", ir.Vec(8, 8), ir.Vec(4, 16), ir.Instruction{Op: ir.OpVExtendSigned, Imm: 1}, 1, 0xff85},
		{"widen-low-half", ir.Vec(4, 32), ir.Vec(2, 64), ir.Instruction{Op: ir.OpVExtendUnsigned}, 1, 0x87868584},
	} {
		t.Run(test.name, func(t *testing.T) {
			op := test.op
			op.Type, op.Bits = test.r, test.r.LaneBits
			got, err := irinterp.Run(laneProgram(test.t, test.r, op, test.lane), nil, nil)
			if err != nil || got.Lo != test.want {
				t.Fatalf("got %#x, %v; want %#x", got.Lo, err, test.want)
			}
		})
	}
}
