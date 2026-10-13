package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// ShuffleLanes returns the vector of x's Type whose lane i is lane lanes[i]
// of the concatenation x‖y. x and y must have the same vector Type, lanes
// must have one entry per lane, and each entry must be below twice the lane
// count: entries from Lanes on select from y.
func (b *Builder) ShuffleLanes(x, y Value, lanes []uint16) (Value, error) {
	values, err := b.materializeValues([]Value{x, y})
	if err != nil {
		return Value{}, err
	}
	x, y = values[0], values[1]
	t := x.Type()
	if !b.valid(x) || !b.valid(y) || t.Kind != ir.VectorKind || y.Type() != t || len(lanes) != int(t.Lanes) {
		return Value{}, fmt.Errorf("ShuffleLanes: need two vectors of one Type and an index per lane (got types %+v/%+v, %d indices): %w", t, y.Type(), len(lanes), ErrInvalidFunction)
	}
	data := make([]byte, len(lanes))
	for i, lane := range lanes {
		if lane >= 2*t.Lanes {
			return Value{}, fmt.Errorf("ShuffleLanes: index %d of lane %d is not below %d: %w", lane, i, 2*t.Lanes, ErrInvalidFunction)
		}
		data[i] = byte(lane)
	}
	return b.append(instruction{Op: ir.OpVShuffle, Type: t, Bits: t.LaneBits, A: x.id, B: y.id, Data: data}), nil
}

// BroadcastLane repeats lane index of v in every lane (a single-input shuffle).
func (b *Builder) BroadcastLane(v Value, index uint16) (Value, error) {
	v, err := b.materialize(v)
	if err != nil {
		return Value{}, err
	}
	t := v.Type()
	if !b.valid(v) || t.Kind != ir.VectorKind || index >= t.Lanes {
		return Value{}, fmt.Errorf("BroadcastLane: need a vector and a lane below its lane count (got type %+v, lane %d): %w", t, index, ErrInvalidFunction)
	}
	data := make([]byte, t.Lanes)
	for i := range data {
		data[i] = byte(index)
	}
	return b.append(instruction{Op: ir.OpVShuffle, Type: t, Bits: t.LaneBits, A: v.id, B: v.id, Data: data}), nil
}

// WidenLanes zero-extends each lane of v, as CastUnsigned does, to a vector
// of the same lane count at twice the lane width. v's lanes must be 8, 16 or
// 32 bits wide, and the result must fit in 512 bits.
func (b *Builder) WidenLanes(v Value) (Value, error) {
	return b.widenLanes("WidenLanes", ir.OpVExtendUnsigned, v)
}

// WidenLanesSigned sign-extends each lane of v to a vector of the same lane
// count at twice the lane width, with WidenLanes's shape rules.
func (b *Builder) WidenLanesSigned(v Value) (Value, error) {
	return b.widenLanes("WidenLanesSigned", ir.OpVExtendSigned, v)
}

func (b *Builder) widenLanes(name string, operation op, v Value) (Value, error) {
	v, err := b.materialize(v)
	if err != nil {
		return Value{}, err
	}
	t := v.Type()
	wide := ir.Type{}
	if t.Kind == ir.VectorKind && t.LaneBits <= 32 {
		wide = ir.Vec(t.Lanes, 2*t.LaneBits)
	}
	if !b.valid(v) || !wide.Valid() {
		return Value{}, fmt.Errorf("%s: need a vector of 8-, 16- or 32-bit lanes whose widened form fits in 512 bits (got type %+v): %w", name, t, ErrInvalidFunction)
	}
	return b.append(instruction{Op: operation, Type: wide, Bits: wide.LaneBits, A: v.id}), nil
}
