package nyanjit

// LeadingZeros counts leading zero bits, returning the scalar width for zero.
func (b *Builder) LeadingZeros(v Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.LeadingZeros(v) })
}

// TrailingZeros counts trailing zero bits, returning the scalar width for zero.
func (b *Builder) TrailingZeros(v Value) Value {
	return takeValue(b, func() (Value, error) { return b.inner.TrailingZeros(v) })
}
