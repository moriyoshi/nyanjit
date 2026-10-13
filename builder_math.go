package nyanjit

// FSin computes the sine of a floating point bit pattern.
func (b *Builder) FSin(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FSin(x))
}

// FCos computes the cosine of a floating point bit pattern.
func (b *Builder) FCos(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FCos(x))
}

// FTan computes the tangent of a floating point bit pattern.
func (b *Builder) FTan(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FTan(x))
}

// FAsin computes the arcsine of a floating point bit pattern.
func (b *Builder) FAsin(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FAsin(x))
}

// FAcos computes the arccosine of a floating point bit pattern.
func (b *Builder) FAcos(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FAcos(x))
}

// FAtan computes the arctangent of a floating point bit pattern.
func (b *Builder) FAtan(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FAtan(x))
}

// FSinh computes the hyperbolic sine of a floating point bit pattern.
func (b *Builder) FSinh(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FSinh(x))
}

// FCosh computes the hyperbolic cosine of a floating point bit pattern.
func (b *Builder) FCosh(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FCosh(x))
}

// FTanh computes the hyperbolic tangent of a floating point bit pattern.
func (b *Builder) FTanh(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FTanh(x))
}

// FAsinh computes the inverse hyperbolic sine of a floating point bit pattern.
func (b *Builder) FAsinh(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FAsinh(x))
}

// FAcosh computes the inverse hyperbolic cosine of a floating point bit pattern.
func (b *Builder) FAcosh(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FAcosh(x))
}

// FAtanh computes the inverse hyperbolic tangent of a floating point bit pattern.
func (b *Builder) FAtanh(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FAtanh(x))
}

// FExp computes the base-e exponential of a floating point bit pattern.
func (b *Builder) FExp(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FExp(x))
}

// FExp2 computes the base-2 exponential of a floating point bit pattern.
func (b *Builder) FExp2(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FExp2(x))
}

// FExpm1 computes the base-e exponential minus one of a floating point bit pattern.
func (b *Builder) FExpm1(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FExpm1(x))
}

// FLog computes the natural logarithm of a floating point bit pattern.
func (b *Builder) FLog(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FLog(x))
}

// FLog2 computes the base-2 logarithm of a floating point bit pattern.
func (b *Builder) FLog2(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FLog2(x))
}

// FLog10 computes the base-10 logarithm of a floating point bit pattern.
func (b *Builder) FLog10(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FLog10(x))
}

// FLog1p computes the natural logarithm of one plus the input of a floating point bit pattern.
func (b *Builder) FLog1p(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FLog1p(x))
}

// FCbrt computes the cube root of a floating point bit pattern.
func (b *Builder) FCbrt(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FCbrt(x))
}

// FFloor computes the floor of a floating point bit pattern.
func (b *Builder) FFloor(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FFloor(x))
}

// FCeil computes the ceiling of a floating point bit pattern.
func (b *Builder) FCeil(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FCeil(x))
}

// FTrunc computes the integer part of a floating point bit pattern.
func (b *Builder) FTrunc(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FTrunc(x))
}

// FRound computes the nearest integer, rounding ties away from zero of a floating point bit pattern.
func (b *Builder) FRound(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FRound(x))
}

// FRoundToEven computes the nearest integer, rounding ties to even of a floating point bit pattern.
func (b *Builder) FRoundToEven(x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FRoundToEven(x))
}

// FPow computes the power of matching floating point bit patterns.
func (b *Builder) FPow(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FPow(x, y))
}

// FAtan2 computes the angle of (x, y), in radians, from floating point bit patterns.
func (b *Builder) FAtan2(y, x Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FAtan2(y, x))
}

// FHypot computes the Euclidean norm of matching floating point bit patterns.
func (b *Builder) FHypot(x, y Value) Value {
	if !b.ready() {
		return Value{}
	}
	return b.okValue(b.inner.FHypot(x, y))
}
