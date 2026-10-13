package builder

import "github.com/moriyoshi/nyanjit/ir"

// ShiftLeftBy shifts by an unsigned Value count. Counts at least the value
// width produce zero.
func (b *Builder) ShiftLeftBy(value, count Value) (Value, error) {
	return b.shiftBy(opShiftLeft, value, count)
}

// ShiftRightBy shifts right logically by an unsigned Value count. Counts at
// least the value width produce zero.
func (b *Builder) ShiftRightBy(value, count Value) (Value, error) {
	return b.shiftBy(opShiftRight, value, count)
}

// ShiftRightArithmeticBy shifts right with sign fill. Counts at least the
// value width produce all sign bits.
func (b *Builder) ShiftRightArithmeticBy(value, count Value) (Value, error) {
	return b.shiftBy(opShiftRightArithmetic, value, count)
}

// RotateLeftBy rotates left by an unsigned Value count modulo the value width.
func (b *Builder) RotateLeftBy(value, count Value) (Value, error) {
	return b.shiftBy(opRotateLeft, value, count)
}

// RotateRightBy rotates right by an unsigned Value count modulo the value width.
func (b *Builder) RotateRightBy(value, count Value) (Value, error) {
	return b.shiftBy(opRotateRight, value, count)
}

// Value-count shifts remain semantic IR until instruction selection. Composite
// values retain the portable decomposition used for register pairs.
func (b *Builder) shiftBy(operation op, value, count Value) (Value, error) {
	var err error
	value, err = b.materialize(value)
	if err != nil {
		return Value{}, err
	}
	count, err = b.materialize(count)
	if err != nil {
		return Value{}, err
	}
	if value.composite != nil {
		return shiftWideBy(b, operation, value, count)
	}
	if !b.valid(value) || !b.valid(count) {
		return Value{}, ErrInvalidFunction
	}
	by := ir.OpShiftLeftBy
	switch operation {
	case opShiftLeft:
	case opShiftRight:
		by = ir.OpShiftRightBy
	case opShiftRightArithmetic:
		by = ir.OpShiftRightArithmeticBy
	case opRotateLeft:
		by = ir.OpRotateLeftBy
	case opRotateRight:
		by = ir.OpRotateRightBy
	default:
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: by, Type: value.Type(), Bits: value.Type().LaneBits, A: value.id, B: count.id}), nil
}
