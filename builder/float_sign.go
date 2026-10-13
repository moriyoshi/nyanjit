package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// FNeg flips the sign bit of a 32- or 64-bit float bit pattern (IEEE negate:
// exact, NaN payload kept).
func (b *Builder) FNeg(x Value) (Value, error) {
	w, err := b.floatSignWidth("FNeg", x)
	if err != nil {
		return Value{}, err
	}
	sign, err := b.ConstBits(w, 1<<(w-1))
	if err != nil {
		return Value{}, err
	}
	return b.Xor(x, sign)
}

// FAbs clears the sign bit (IEEE abs).
func (b *Builder) FAbs(x Value) (Value, error) {
	w, err := b.floatSignWidth("FAbs", x)
	if err != nil {
		return Value{}, err
	}
	magnitude, err := b.ConstBits(w, 1<<(w-1)-1)
	if err != nil {
		return Value{}, err
	}
	return b.And(x, magnitude)
}

// FCopySign returns x with y's sign bit; x and y have the same width.
func (b *Builder) FCopySign(x, y Value) (Value, error) {
	w, err := b.floatSignWidth("FCopySign", x)
	if err != nil {
		return Value{}, err
	}
	if v, err := b.floatSignWidth("FCopySign", y); err != nil || v != w {
		return Value{}, fmt.Errorf("FCopySign: operands must have the same width (%d and %d bits): %w", w, y.Type().LaneBits, ErrInvalidFunction)
	}
	magnitude, err := b.FAbs(x)
	if err != nil {
		return Value{}, err
	}
	signMask, err := b.ConstBits(w, 1<<(w-1))
	if err != nil {
		return Value{}, err
	}
	sign, err := b.And(y, signMask)
	if err != nil {
		return Value{}, err
	}
	return b.Or(magnitude, sign)
}

// floatSignWidth returns the width of x, which must be a 32- or 64-bit
// scalar of b.
func (b *Builder) floatSignWidth(operation string, x Value) (uint8, error) {
	if b == nil || x.Type().Kind != ir.ScalarKind || x.composite != nil || !b.validExpression(x) || x.Type().LaneBits != 32 && x.Type().LaneBits != 64 {
		return 0, fmt.Errorf("%s: operand must be in scope and have 32 or 64 bits: %w", operation, ErrInvalidFunction)
	}
	return x.Type().LaneBits, nil
}
