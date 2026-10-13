package nyanjit

// ReverseBytes reverses the byte order of a 16-, 32-, 64- or 128-bit integer.
func (b *Builder) ReverseBytes(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.ReverseBytes(x))
}
