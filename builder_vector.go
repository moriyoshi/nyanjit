package nyanjit

import "github.com/moriyoshi/nyanjit/builder"

// VMask is a logical boolean per vector lane.
type VMask = builder.VMask

// VLoad reads an unaligned vector in memory lane order.
func (b *Builder) VLoad(t Type, address Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.VLoad(t, address) })
}

// VStore writes exactly the vector's bytes.
func (b *Builder) VStore(address, value Value) {
	do(b, func() error { return b.inner.VStore(address, value) })
}

// Splat repeats a matching scalar across a vector's lanes.
func (b *Builder) Splat(t Type, scalar Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.Splat(t, scalar) })
}

// ConstVector copies canonical lane bytes into an immutable vector constant.
func (b *Builder) ConstVector(t Type, data []byte) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ConstVector(t, data) })
}

// ExtractLane reads the scalar at a constant lane index.
func (b *Builder) ExtractLane(v Value, index uint16) Value {
	return takeValue(b, func() (Value, error) { return b.inner.ExtractLane(v, index) })
}

// InsertLane replaces a constant-index lane with a matching scalar.
func (b *Builder) InsertLane(v Value, index uint16, s Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.InsertLane(v, index, s) })
}

// VCompare produces a boolean per lane using an integer comparison.
func (b *Builder) VCompare(x Value, condition BranchCondition, y Value) VMask {
	value := takeValue(b, func() (Value, error) {
		m, err := b.inner.VCompare(x, condition, y)
		return m.Value(), err
	})
	return value.Mask()
}

// MaskBits packs mask lane i into scalar bit i.
func (b *Builder) MaskBits(m VMask) Value {
	return takeValue(b, func() (Value, error) { return b.inner.MaskBits(m) })
}

// BindParamsTypes binds complete scalar, vector and mask shapes to a label.
func (b *Builder) BindParamsTypes(label Label, types ...Type) []Value {
	return takeValues(b, len(types), func() ([]Value, error) { return b.inner.BindParamsTypes(label, types...) })
}
