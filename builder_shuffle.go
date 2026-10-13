package nyanjit

// ShuffleLanes returns the vector whose lane i is lane lanes[i] of x‖y. x
// and y have one vector Type; lanes has an entry per lane, each below twice
// the lane count.
func (b *Builder) ShuffleLanes(x, y Value, lanes []uint16) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ShuffleLanes(x, y, lanes))
}

// BroadcastLane repeats lane index of v in every lane.
func (b *Builder) BroadcastLane(v Value, index uint16) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.BroadcastLane(v, index))
}

// WidenLanes zero-extends each 8-, 16- or 32-bit lane of v to twice its
// width, keeping the lane count; the result must fit in 512 bits.
func (b *Builder) WidenLanes(v Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.WidenLanes(v))
}

// WidenLanesSigned sign-extends each lane of v to twice its width, with
// WidenLanes's shape rules.
func (b *Builder) WidenLanesSigned(v Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.WidenLanesSigned(v))
}
