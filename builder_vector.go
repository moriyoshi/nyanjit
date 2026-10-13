package nyanjit

import (
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/ir"
)

// VMask is a logical boolean per vector lane.
type VMask = builder.VMask

// VLoad reads an unaligned vector in memory lane order.
func (b *Builder) VLoad(t ir.Type, address Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.VLoad(t, address))
}

// VStore writes exactly the vector's bytes.
func (b *Builder) VStore(address, value Value) {
	if b.ready() {
		b.okDo(b.inner.VStore(address, value))
	}
}

// Splat repeats a matching scalar across a vector's lanes.
func (b *Builder) Splat(t ir.Type, scalar Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Splat(t, scalar))
}

// ConstVector copies canonical lane bytes into an immutable vector constant.
func (b *Builder) ConstVector(t ir.Type, data []byte) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ConstVector(t, data))
}

// ExtractLane reads the scalar at a constant lane index.
func (b *Builder) ExtractLane(v Value, index uint16) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ExtractLane(v, index))
}

// InsertLane replaces a constant-index lane with a matching scalar.
func (b *Builder) InsertLane(v Value, index uint16, s Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.InsertLane(v, index, s))
}

// VCompare produces a boolean per lane using an integer comparison.
func (b *Builder) VCompare(x Value, condition ir.BranchCondition, y Value) VMask {
	value := takeValue(b, func() (Value, error) {
		m, err := b.inner.VCompare(x, condition, y)
		return m.Value(), err
	})
	return value.Mask()
}

// MaskBits packs mask lane i into scalar bit i.
func (b *Builder) MaskBits(m VMask) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.MaskBits(m))
}

// CarrylessMul multiplies a selected 64-bit lane from x by one from y in every
// 128-bit chunk. Selectors are 0 or 1. Matching vector Types must contain an
// even number of 64-bit lanes; output chunks hold low then high product words.
func (b *Builder) CarrylessMul(x, y Value, xLane, yLane uint16) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.CarrylessMul(x, y, xLane, yLane))
}
