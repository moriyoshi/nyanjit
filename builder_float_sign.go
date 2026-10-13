package nyanjit

// FNeg flips the sign bit of a 32- or 64-bit float bit pattern (IEEE negate:
// exact, NaN payload kept).
func (b *Builder) FNeg(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FNeg(x))
}

// FAbs clears the sign bit of a 32- or 64-bit float bit pattern (IEEE abs).
func (b *Builder) FAbs(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FAbs(x))
}

// FCopySign returns x with y's sign bit; x and y have the same width.
func (b *Builder) FCopySign(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FCopySign(x, y))
}
