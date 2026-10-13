package builder

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
)

// LeadingZeros returns the number of zero bits before the first set bit.
// Zero returns the input width. The result retains the input's scalar width.
func (b *Builder) LeadingZeros(v Value) (Value, error) { return b.countZeros(ir.OpLeadingZeros, v) }

// TrailingZeros returns the number of zero bits after the last set bit.
// Zero returns the input width. Supports scalar widths 8, 16, 32 and 64.
func (b *Builder) TrailingZeros(v Value) (Value, error) { return b.countZeros(ir.OpTrailingZeros, v) }
func (b *Builder) countZeros(op ir.Opcode, v Value) (Value, error) {
	v, err := b.materialize(v)
	if err != nil {
		return Value{}, err
	}
	if !b.valid(v) || v.Type().Kind != ir.ScalarKind || v.Bits() > 64 {
		return Value{}, fmt.Errorf("count zeros requires an in-scope scalar of at most 64 bits: %w", ErrInvalidFunction)
	}
	return b.append(instruction{Op: op, Type: v.Type(), Bits: v.Type().LaneBits, A: v.id}), nil
}
