package nyanjit

// MulHigh returns the upper half of the full unsigned product of
// equal-width integers x and y.
func (b *Builder) MulHigh(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.MulHigh(x, y))
}

// MulHighSigned returns the upper half of the full signed product of
// equal-width integers x and y.
func (b *Builder) MulHighSigned(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.MulHighSigned(x, y))
}
