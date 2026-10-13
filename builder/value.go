package builder

// Value is a typed scalar, vector, or mask produced by a Builder. Its fields are private
// so values can only be used with the function that created them.
type Value struct {
	builder    *Builder
	id         int
	composite  *valueComposite
	expression *valueExpression
}

type valueComposite struct {
	parts []Value
	width uint16
}

// Valid reports whether a value or every leaf of a pure expression can be used
// in the Builder's current scope.
func (b *Builder) Valid(value Value) bool { return b.validExpression(value) }

// Type reports logical shape, independently of its eventual register homes.
func (v Value) Type() Type {
	if v.expression != nil {
		if v.builder == nil || v.builder.finished {
			return Type{}
		}
		if v.expression.typ.Valid() {
			return v.expression.typ
		}
		return Scalar(v.expression.bits)
	}
	if v.composite != nil {
		if v.builder == nil {
			return Type{}
		}
		width := uint16(0)
		for _, part := range v.composite.parts {
			if part.builder != v.builder || !part.Type().Valid() {
				return Type{}
			}
			width += part.Bits()
		}
		if width != v.composite.width {
			return Type{}
		}
		return Scalar(uint8(width))
	}
	if v.builder == nil || v.id < 0 || v.id >= len(v.builder.code) {
		return Type{}
	}
	return v.builder.code[v.id].ValueType()
}

// Bits returns total logical width, including vector widths up to 512 bits.
func (v Value) Bits() uint16 { return v.Type().Bits() }

// compositeValue joins logical components without assigning physical registers.
func compositeValue(parts ...Value) Value {
	if len(parts) == 1 {
		return parts[0]
	}
	if len(parts) == 0 {
		return Value{}
	}
	width := 0
	for _, part := range parts {
		width += int(part.Bits())
	}
	if width == 0 || width > 128 {
		return Value{}
	}
	return Value{builder: parts[0].builder, id: -1, composite: &valueComposite{parts: append([]Value(nil), parts...), width: uint16(width)}}
}

func (v Value) components() []Value {
	if v.composite == nil {
		return []Value{v}
	}
	return v.composite.parts
}
