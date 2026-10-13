package builder

import "github.com/moriyoshi/nyanjit/ir"

// MulHigh returns the upper half of the full unsigned product of
// equal-width integers x and y.
func (b *Builder) MulHigh(x, y Value) (Value, error) {
	return b.mulHigh(ir.OpMulHigh, x, y)
}

// MulHighSigned returns the upper half of the full signed product of
// equal-width integers x and y.
func (b *Builder) MulHighSigned(x, y Value) (Value, error) {
	return b.mulHigh(ir.OpMulHighSigned, x, y)
}

func (b *Builder) mulHigh(operation op, x, y Value) (Value, error) {
	x, y, err := b.scalarPair(operation, x, y)
	if err != nil {
		return Value{}, err
	}
	return b.append(instruction{Op: operation, A: x.id, B: y.id, Bits: x.Type().LaneBits}), nil
}
