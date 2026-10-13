package builder

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
)

// requireScalars rejects vector/mask handles before a scalar operation changes IR.
func requireScalars(operation string, values ...Value) error {
	for _, v := range values {
		if v.Type().Kind != ir.ScalarKind {
			return fmt.Errorf("%s: value %d must be scalar (got %+v): %w", operation, v.id, v.Type(), ErrInvalidFunction)
		}
	}
	return nil
}

// Concat joins integer values in least-significant-first order. The total
// width must be 8, 16, 32, 64, or 128 bits.
func (b *Builder) Concat(parts ...Value) (Value, error) {
	if err := requireScalars("Concat", parts...); err != nil {
		return Value{}, err
	}
	var err error
	parts, err = b.materializeValues(parts)
	if err != nil {
		return Value{}, err
	}
	if len(parts) == 0 {
		return Value{}, ErrInvalidFunction
	}
	width := 0
	for _, part := range parts {
		if !b.valid(part) {
			return Value{}, ErrInvalidFunction
		}
		width += int(part.Type().LaneBits)
	}
	if width != 8 && width != 16 && width != 32 && width != 64 && width != 128 {
		return Value{}, ErrBackendLimit
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	if width == 128 && len(parts) == 2 && parts[0].Type().LaneBits == 64 && parts[1].Type().LaneBits == 64 {
		return compositeValue(parts...), nil
	}
	low, err := b.Const(0)
	if err != nil {
		return Value{}, err
	}
	high := low
	position := 0
	for _, part := range parts {
		word, err := b.CastUnsigned(part, 64)
		if err != nil {
			return Value{}, err
		}
		if position < 64 {
			shifted := word
			if position != 0 {
				shifted, err = b.ShiftLeft(word, uint8(position))
				if err != nil {
					return Value{}, err
				}
			}
			low, err = b.Or(low, shifted)
			if err != nil {
				return Value{}, err
			}
			if position+int(part.Type().LaneBits) > 64 {
				spill, err := b.ShiftRight(word, uint8(64-position))
				if err != nil {
					return Value{}, err
				}
				high, err = b.Or(high, spill)
				if err != nil {
					return Value{}, err
				}
			}
		} else {
			shifted := word
			if position != 64 {
				shifted, err = b.ShiftLeft(word, uint8(position-64))
				if err != nil {
					return Value{}, err
				}
			}
			high, err = b.Or(high, shifted)
			if err != nil {
				return Value{}, err
			}
		}
		position += int(part.Type().LaneBits)
	}
	if width == 128 {
		return compositeValue(low, high), nil
	}
	return b.CastUnsigned(low, uint8(width))
}

// ExtractBits returns a width-limited slice starting at offset from the low
// bit. A 128-bit value may be extracted in widths up to 64 bits.
func (b *Builder) ExtractBits(value Value, offset, bits uint8) (Value, error) {
	if err := requireScalars("ExtractBits", value); err != nil {
		return Value{}, err
	}
	var err error
	value, err = b.materialize(value)
	if err != nil {
		return Value{}, err
	}
	if !validScalarBits(bits) || int(offset)+int(bits) > int(value.Type().LaneBits) {
		return Value{}, ErrInvalidFunction
	}
	if value.composite == nil {
		if !b.valid(value) {
			return Value{}, ErrInvalidFunction
		}
		shifted := value
		var err error
		if offset != 0 {
			shifted, err = b.ShiftRight(value, offset)
			if err != nil {
				return Value{}, err
			}
		}
		return b.CastUnsigned(shifted, bits)
	}
	if !b.validComposite(value) || len(value.composite.parts) != 2 || value.composite.parts[0].Type().LaneBits != 64 || value.composite.parts[1].Type().LaneBits != 64 {
		return Value{}, ErrBackendLimit
	}
	lo, hi := value.composite.parts[0], value.composite.parts[1]
	var result Value
	if offset >= 64 {
		if offset == 64 {
			result = hi
		} else {
			result, err = b.ShiftRight(hi, offset-64)
		}
	} else {
		result = lo
		if offset != 0 {
			result, err = b.ShiftRight(lo, offset)
		}
		if err == nil && int(offset)+int(bits) > 64 {
			upper, shiftErr := b.ShiftLeft(hi, 64-offset)
			if shiftErr != nil {
				return Value{}, shiftErr
			}
			result, err = b.Or(result, upper)
		}
	}
	if err != nil {
		return Value{}, err
	}
	return b.CastUnsigned(result, bits)
}
