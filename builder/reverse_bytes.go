package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// ReverseBytes reverses the byte order of a 16-, 32-, 64- or 128-bit integer.
// A 128-bit value reverses both words and swaps them.
func (b *Builder) ReverseBytes(x Value) (Value, error) {
	var err error
	if x, err = b.materialize(x); err != nil {
		return Value{}, err
	}
	if err := requireScalars("ReverseBytes", x); err != nil {
		return Value{}, err
	}
	if x.composite != nil {
		if !b.validComposite(x) {
			return Value{}, b.invalidValue(x)
		}
		low, err := b.ReverseBytes(wideHigh(x))
		if err != nil {
			return Value{}, err
		}
		high, err := b.ReverseBytes(wideLow(x))
		if err != nil {
			return Value{}, err
		}
		return compositeValue(low, high), nil
	}
	if !b.valid(x) {
		return Value{}, b.invalidValue(x)
	}
	w := x.Type().LaneBits
	if w != 16 && w != 32 && w != 64 {
		return Value{}, fmt.Errorf("ReverseBytes: operand has %d bits; want 16, 32, 64 or 128: %w", w, ErrInvalidFunction)
	}
	return b.append(instruction{Op: ir.OpReverseBytes, A: x.id, Bits: w}), nil
}
