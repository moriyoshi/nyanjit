package nyanjit

// LeadingZeros counts leading zero bits, returning the scalar width for zero.
func (b *Builder) LeadingZeros(v Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.LeadingZeros(v))
}

// TrailingZeros counts trailing zero bits, returning the scalar width for zero.
func (b *Builder) TrailingZeros(v Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.TrailingZeros(v))
}
