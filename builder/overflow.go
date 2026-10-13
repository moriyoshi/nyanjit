package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// Overflow tests signed overflow of the Add, Sub or Mul that defined value: the
// exact result does not fit value's width as a two's complement integer.
//
// A sum s = a+b overflows when IsNegative((a^s) & (b^s)), a difference
// s = a-b when IsNegative((a^b) & (a^s)), and a product p = a*b when
// MulHighSigned(a, b) != p >> (w-1) arithmetically. The predicate is anchored
// to value, so a target can branch on the overflow flag of the arithmetic
// when it is still live. PredicateValue(Overflow(v), w) is its value.
func (b *Builder) Overflow(value Value) (Predicate, error) {
	var err error
	if value, err = b.materialize(value); err != nil {
		return Predicate{}, err
	}
	if b == nil || value.composite != nil || value.Type().Kind != ir.ScalarKind || !b.valid(value) {
		return Predicate{}, fmt.Errorf("Overflow: value must be an in-scope scalar of at most 64 bits: %w", ErrInvalidFunction)
	}
	record := b.values[value.id]
	x, y := Value{builder: b, id: int(record.lo)}, Value{builder: b, id: int(record.hi)}
	w := value.Type().LaneBits
	var predicate Predicate
	switch record.op {
	case opAdd, opSub:
		// Sum: both operands differ in sign from the result. Difference:
		// the operands differ in sign, and the result differs from x.
		first, second := Value{}, Value{}
		if record.op == opAdd {
			if first, err = b.Xor(x, value); err == nil {
				second, err = b.Xor(y, value)
			}
		} else if first, err = b.Xor(x, y); err == nil {
			second, err = b.Xor(x, value)
		}
		if err != nil {
			return Predicate{}, err
		}
		both, err := b.And(first, second)
		if err != nil {
			return Predicate{}, err
		}
		if predicate, err = b.IsNegative(both); err != nil {
			return Predicate{}, err
		}
	case opMul:
		high, err := b.MulHighSigned(x, y)
		if err != nil {
			return Predicate{}, err
		}
		// The product's sign, repeated: an arithmetic shift at 64 bits, and
		// 0 - (p >> (w-1)) at a narrower width, which stays zero-extended.
		var sign Value
		if w == 64 {
			sign, err = b.ShiftRightArithmetic(value, w-1)
		} else if sign, err = b.ShiftRight(value, w-1); err == nil {
			var zero Value
			if zero, err = b.ConstBits(w, 0); err == nil {
				sign, err = b.Sub(zero, sign)
			}
		}
		if err != nil {
			return Predicate{}, err
		}
		if predicate, err = b.Compare(high, ir.BranchNotEqual, sign); err != nil {
			return Predicate{}, err
		}
	default:
		return Predicate{}, fmt.Errorf("Overflow: value %d is not the result of Add, Sub or Mul: %w", value.id, ErrInvalidFunction)
	}
	predicate.node.anchor, predicate.node.hasAnchor = value.id, true
	return predicate, nil
}
