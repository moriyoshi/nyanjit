package nyanjit

import "github.com/moriyoshi/nyanjit/ir"

// SetDivisionSemantics sets what later Div, DivSigned, Rem and RemSigned
// operations do for a zero divisor and for signed MIN / -1. The default is
// ir.DivisionZero.
func (b *Builder) SetDivisionSemantics(s ir.DivisionSemantics) { b.inner.SetDivisionSemantics(s) }

// SetConversionSemantics sets what later FToSigned and FToUnsigned
// operations return for NaN and out-of-range values. The default is
// ir.ConversionSaturate.
func (b *Builder) SetConversionSemantics(s ir.ConversionSemantics) {
	b.inner.SetConversionSemantics(s)
}

// Div divides equal-width unsigned integers toward zero.
func (b *Builder) Div(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Div(x, y))
}

// DivSigned divides equal-width signed integers toward zero.
func (b *Builder) DivSigned(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.DivSigned(x, y))
}

// Rem returns the unsigned remainder of x divided by y.
func (b *Builder) Rem(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.Rem(x, y))
}

// RemSigned returns the signed remainder of x divided by y, with x's sign.
func (b *Builder) RemSigned(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.RemSigned(x, y))
}

// DivWith is Div with semantics s.
func (b *Builder) DivWith(s ir.DivisionSemantics, x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.DivWith(s, x, y))
}

// DivSignedWith is DivSigned with semantics s.
func (b *Builder) DivSignedWith(s ir.DivisionSemantics, x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.DivSignedWith(s, x, y))
}

// RemWith is Rem with semantics s.
func (b *Builder) RemWith(s ir.DivisionSemantics, x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.RemWith(s, x, y))
}

// RemSignedWith is RemSigned with semantics s.
func (b *Builder) RemSignedWith(s ir.DivisionSemantics, x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.RemSignedWith(s, x, y))
}

// FFromSigned converts a 32- or 64-bit signed integer to a binary32 or
// binary64 bit pattern of bits.
func (b *Builder) FFromSigned(x Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FFromSigned(x, bits))
}

// FFromUnsigned converts a 32- or 64-bit unsigned integer to a binary32 or
// binary64 bit pattern of bits.
func (b *Builder) FFromUnsigned(x Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FFromUnsigned(x, bits))
}

// FToSigned truncates a floating point bit pattern toward zero to a signed
// integer of bits.
func (b *Builder) FToSigned(x Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FToSigned(x, bits))
}

// FToUnsigned truncates a floating point bit pattern toward zero to an
// unsigned integer of bits.
func (b *Builder) FToUnsigned(x Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FToUnsigned(x, bits))
}

// FToSignedWith is FToSigned with semantics s.
func (b *Builder) FToSignedWith(s ir.ConversionSemantics, x Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FToSignedWith(s, x, bits))
}

// FToUnsignedWith is FToUnsigned with semantics s.
func (b *Builder) FToUnsignedWith(s ir.ConversionSemantics, x Value, bits uint8) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FToUnsignedWith(s, x, bits))
}
