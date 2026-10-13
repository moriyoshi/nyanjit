package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

func TestCarrylessMulRejectsInvalidOperands(t *testing.T) {
	for _, test := range []struct {
		name         string
		typ          ir.Type
		xLane, yLane uint16
		foreign      bool
	}{
		{"x-selector", ir.Vec(2, 64), 2, 0, false},
		{"y-selector", ir.Vec(2, 64), 0, 2, false},
		{"large-selector", ir.Vec(2, 64), 65535, 65535, false},
		{"lane-width", ir.Vec(4, 32), 0, 0, false},
		{"odd-lanes", ir.Vec(3, 64), 0, 0, false},
		{"foreign-builder", ir.Vec(2, 64), 0, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := NewBuilder()
			x := mustVector(t, b, test.typ)
			other := b
			if test.foreign {
				other = NewBuilder()
			}
			y := mustVector(t, other, test.typ)
			if _, err := b.CarrylessMul(x, y, test.xLane, test.yLane); !errors.Is(err, ErrInvalidFunction) {
				t.Fatalf("CarrylessMul=%v", err)
			}
			if got := x.CarrylessMul(y, test.xLane, test.yLane); got.Type().Valid() {
				t.Fatalf("invalid expression accepted: %+v", got.Type())
			}
		})
	}
	b := NewBuilder()
	x, y := mustVector(t, b, ir.Vec(2, 64)), mustVector(t, b, ir.Vec(4, 64))
	if _, err := b.CarrylessMul(x, y, 0, 0); !errors.Is(err, ErrInvalidFunction) {
		t.Fatal("different vector shapes accepted")
	}
}

func TestCarrylessMulDeferredExpression(t *testing.T) {
	b := NewBuilder()
	x, y := mustVector(t, b, ir.Vec(2, 64)), mustVector(t, b, ir.Vec(2, 64))
	before := len(b.code)
	v := x.CarrylessMul(y, 1, 0).Xor(y).CarrylessMul(x, 0, 1)
	if !b.Valid(v) || v.id != -1 || v.expression == nil || v.Type() != ir.Vec(2, 64) {
		t.Fatal("expression was not deferred")
	}
	if len(b.code) != before {
		t.Fatal("expression construction emitted IR eagerly")
	}
	// Its leaves and both polynomial products materialize only at consumption.
	lane, err := b.ExtractLane(v, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(lane); err != nil {
		t.Fatal(err)
	}
	p, err := testFinishRaw(b)
	if err != nil {
		t.Fatal(err)
	}
	selectors := []uint64{}
	for _, ins := range p.Instructions {
		if ins.Op == ir.OpVCarrylessMul {
			selectors = append(selectors, ins.Imm)
		}
	}
	if len(selectors) != 2 || selectors[0] != 1 || selectors[1] != 2 {
		t.Fatalf("materialized selectors=%v", selectors)
	}
}
