package builder

import (
	"errors"
	"slices"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

// lastInstruction finishes b returning a scalar and returns the
// sealed program's instruction of opcode op.
func lastInstruction(t *testing.T, b *Builder, op ir.Opcode) ir.Instruction {
	t.Helper()
	if err := b.Return(mustScalar(t, b, 64)); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var found ir.Instruction
	for _, ins := range program.Instructions() {
		if ins.Op == op {
			found = ins
		}
	}
	if found.Op != op {
		t.Fatalf("no %v in the program", op)
	}
	return found
}

func TestShuffleLanes(t *testing.T) {
	b := NewBuilder()
	x, y := mustVector(t, b, ir.Vec(4, 32)), mustVector(t, b, ir.Vec(4, 32))
	if _, err := b.ShuffleLanes(x, y, []uint16{7, 0, 5, 2}); err != nil {
		t.Fatal(err)
	}
	ins := lastInstruction(t, b, ir.OpVShuffle)
	if ins.Type != ir.Vec(4, 32) || ins.Bits != 32 || !slices.Equal(ins.Data, []byte{7, 0, 5, 2}) || ins.A == ins.B {
		t.Fatalf("shuffle %+v", ins)
	}

	for _, test := range []struct {
		name  string
		yType ir.Type
		lanes []uint16
	}{
		{"index", ir.Vec(4, 32), []uint16{0, 1, 2, 8}},
		{"large-index", ir.Vec(4, 32), []uint16{0, 1, 2, 0x100}},
		{"short", ir.Vec(4, 32), []uint16{0, 1, 2}},
		{"long", ir.Vec(4, 32), []uint16{0, 1, 2, 3, 4}},
		{"types", ir.Vec(2, 64), []uint16{0, 1, 2, 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := NewBuilder()
			x, y := mustVector(t, b, ir.Vec(4, 32)), mustVector(t, b, test.yType)
			if _, err := b.ShuffleLanes(x, y, test.lanes); !errors.Is(err, ErrInvalidFunction) {
				t.Fatalf("ShuffleLanes=%v", err)
			}
		})
	}
	b = NewBuilder()
	s := mustScalar(t, b, 32)
	if _, err := b.ShuffleLanes(s, s, []uint16{0}); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("scalar shuffle: %v", err)
	}
	other := NewBuilder()
	if _, err := b.ShuffleLanes(mustVector(t, b, ir.Vec(2, 64)), mustVector(t, other, ir.Vec(2, 64)), []uint16{0, 3}); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("foreign operand: %v", err)
	}
}

func TestBroadcastLane(t *testing.T) {
	b := NewBuilder()
	v := mustVector(t, b, ir.Vec(8, 16))
	if _, err := b.BroadcastLane(v, 5); err != nil {
		t.Fatal(err)
	}
	ins := lastInstruction(t, b, ir.OpVShuffle)
	if ins.Type != ir.Vec(8, 16) || ins.A != ins.B || !slices.Equal(ins.Data, []byte{5, 5, 5, 5, 5, 5, 5, 5}) {
		t.Fatalf("broadcast %+v", ins)
	}
	b = NewBuilder()
	if _, err := b.BroadcastLane(mustVector(t, b, ir.Vec(8, 16)), 8); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("lane out of range: %v", err)
	}
	if _, err := b.BroadcastLane(mustScalar(t, b, 16), 0); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("scalar broadcast: %v", err)
	}
}

func TestWidenLanes(t *testing.T) {
	for _, test := range []struct {
		from, to ir.Type
	}{
		{ir.Vec(16, 8), ir.Vec(16, 16)},
		{ir.Vec(2, 16), ir.Vec(2, 32)},
		{ir.Vec(8, 32), ir.Vec(8, 64)},
	} {
		for _, signed := range []bool{false, true} {
			b := NewBuilder()
			widen, op := b.WidenLanes, ir.OpVExtendUnsigned
			if signed {
				widen, op = b.WidenLanesSigned, ir.OpVExtendSigned
			}
			w, err := widen(mustVector(t, b, test.from))
			if err != nil || w.Type() != test.to {
				t.Fatalf("%v: %+v, %v", test.from, w.Type(), err)
			}
			if ins := lastInstruction(t, b, op); ins.Type != test.to || ins.Bits != test.to.LaneBits || ins.Imm != 0 {
				t.Fatalf("extend %+v", ins)
			}
		}
	}
	for _, from := range []ir.Type{ir.Vec(4, 64), ir.Vec(16, 32), ir.Vec(64, 8)} {
		b := NewBuilder()
		v := mustVector(t, b, from)
		if _, err := b.WidenLanes(v); !errors.Is(err, ErrInvalidFunction) {
			t.Fatalf("WidenLanes(%v)=%v", from, err)
		}
		if _, err := b.WidenLanesSigned(v); !errors.Is(err, ErrInvalidFunction) {
			t.Fatalf("WidenLanesSigned(%v)=%v", from, err)
		}
	}
	b := NewBuilder()
	if _, err := b.WidenLanes(mustScalar(t, b, 32)); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("scalar widen: %v", err)
	}
}
