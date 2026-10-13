package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// SetDivisionSemantics sets what later Div, DivSigned, Rem and RemSigned
// operations do for a zero divisor and for signed MIN / -1. The default is
// ir.DivisionZero.
func (b *Builder) SetDivisionSemantics(s ir.DivisionSemantics) {
	if b != nil {
		b.division = s
	}
}

// SetConversionSemantics sets what later FToSigned and FToUnsigned
// operations return for NaN and out-of-range values. The default is
// ir.ConversionSaturate.
func (b *Builder) SetConversionSemantics(s ir.ConversionSemantics) {
	if b != nil {
		b.conversion = s
	}
}

func (b *Builder) divisionDefault() ir.DivisionSemantics {
	if b == nil {
		return ir.DivisionZero
	}
	return b.division
}

func (b *Builder) conversionDefault() ir.ConversionSemantics {
	if b == nil {
		return ir.ConversionSaturate
	}
	return b.conversion
}

// Div divides x by y as unsigned integers of their common width (8, 16, 32
// or 64 bits), toward zero, with the builder's division semantics.
func (b *Builder) Div(x, y Value) (Value, error) {
	return b.divide(ir.OpDiv, b.divisionDefault(), x, y)
}

// DivSigned divides x by y as signed integers, toward zero, with the
// builder's division semantics.
func (b *Builder) DivSigned(x, y Value) (Value, error) {
	return b.divide(ir.OpDivSigned, b.divisionDefault(), x, y)
}

// Rem returns the unsigned remainder of x divided by y, with the builder's
// division semantics.
func (b *Builder) Rem(x, y Value) (Value, error) {
	return b.divide(ir.OpRem, b.divisionDefault(), x, y)
}

// RemSigned returns the signed remainder of x divided by y, which has x's
// sign, with the builder's division semantics.
func (b *Builder) RemSigned(x, y Value) (Value, error) {
	return b.divide(ir.OpRemSigned, b.divisionDefault(), x, y)
}

// DivWith is Div with semantics s.
func (b *Builder) DivWith(s ir.DivisionSemantics, x, y Value) (Value, error) {
	return b.divide(ir.OpDiv, s, x, y)
}

// DivSignedWith is DivSigned with semantics s.
func (b *Builder) DivSignedWith(s ir.DivisionSemantics, x, y Value) (Value, error) {
	return b.divide(ir.OpDivSigned, s, x, y)
}

// RemWith is Rem with semantics s.
func (b *Builder) RemWith(s ir.DivisionSemantics, x, y Value) (Value, error) {
	return b.divide(ir.OpRem, s, x, y)
}

// RemSignedWith is RemSigned with semantics s.
func (b *Builder) RemSignedWith(s ir.DivisionSemantics, x, y Value) (Value, error) {
	return b.divide(ir.OpRemSigned, s, x, y)
}

func (b *Builder) divide(operation op, s ir.DivisionSemantics, x, y Value) (Value, error) {
	if !s.Valid() {
		return Value{}, fmt.Errorf("%v: division semantics %d is not defined: %w", operation, s, ErrInvalidFunction)
	}
	x, y, err := b.scalarPair(operation, x, y)
	if err != nil {
		return Value{}, err
	}
	return b.append(instruction{Op: operation, A: x.id, B: y.id, Bits: x.Type().LaneBits, Imm: uint64(s)}), nil
}

// scalarPair materializes the operands of a two-input scalar operation and
// checks that they are same-width scalars of at most 64 bits.
func (b *Builder) scalarPair(operation op, x, y Value) (Value, Value, error) {
	var err error
	if x, err = b.materialize(x); err != nil {
		return Value{}, Value{}, err
	}
	if y, err = b.materialize(y); err != nil {
		return Value{}, Value{}, err
	}
	if x.Type().Kind != ir.ScalarKind || y.Type().Kind != ir.ScalarKind || x.composite != nil || y.composite != nil {
		return Value{}, Value{}, fmt.Errorf("%v: operands must be scalars of at most 64 bits: %w", operation, ErrInvalidFunction)
	}
	if b == nil || b.returned || !b.valid(x) || !b.valid(y) {
		return Value{}, Value{}, b.invalidOperands(x, y)
	}
	if x.Type() != y.Type() {
		return Value{}, Value{}, fmt.Errorf("%v: operands have widths %d and %d bits (values %d and %d): %w", operation, x.Type().LaneBits, y.Type().LaneBits, x.id, y.id, ErrInvalidFunction)
	}
	return x, y, nil
}

// FFromSigned converts signed integer x (32 or 64 bits) to a binary32 or
// binary64 bit pattern (bits), rounding in the target's current mode.
func (b *Builder) FFromSigned(x Value, bits uint8) (Value, error) {
	return b.convert(ir.OpFFromSigned, 0, x, bits)
}

// FFromUnsigned converts unsigned integer x (32 or 64 bits) to a binary32 or
// binary64 bit pattern (bits), rounding in the target's current mode.
func (b *Builder) FFromUnsigned(x Value, bits uint8) (Value, error) {
	return b.convert(ir.OpFFromUnsigned, 0, x, bits)
}

// FToSigned truncates binary32 or binary64 x toward zero to a signed integer
// of bits (32 or 64), with the builder's conversion semantics.
func (b *Builder) FToSigned(x Value, bits uint8) (Value, error) {
	return b.convert(ir.OpFToSigned, b.conversionDefault(), x, bits)
}

// FToUnsigned truncates binary32 or binary64 x toward zero to an unsigned
// integer of bits (32 or 64), with the builder's conversion semantics.
func (b *Builder) FToUnsigned(x Value, bits uint8) (Value, error) {
	return b.convert(ir.OpFToUnsigned, b.conversionDefault(), x, bits)
}

// FToSignedWith is FToSigned with semantics s.
func (b *Builder) FToSignedWith(s ir.ConversionSemantics, x Value, bits uint8) (Value, error) {
	return b.convert(ir.OpFToSigned, s, x, bits)
}

// FToUnsignedWith is FToUnsigned with semantics s.
func (b *Builder) FToUnsignedWith(s ir.ConversionSemantics, x Value, bits uint8) (Value, error) {
	return b.convert(ir.OpFToUnsigned, s, x, bits)
}

func (b *Builder) convert(operation op, s ir.ConversionSemantics, x Value, bits uint8) (Value, error) {
	if !s.Valid() {
		return Value{}, fmt.Errorf("%v: conversion semantics %d is not defined: %w", operation, s, ErrInvalidFunction)
	}
	var err error
	if x, err = b.materialize(x); err != nil {
		return Value{}, err
	}
	if b == nil || b.returned || !b.valid(x) || x.Type().Kind != ir.ScalarKind || x.composite != nil ||
		x.Type().LaneBits != 32 && x.Type().LaneBits != 64 || bits != 32 && bits != 64 {
		return Value{}, fmt.Errorf("%v: operand must be in scope and have 32 or 64 bits, and the result 32 or 64 bits: %w", operation, ErrInvalidFunction)
	}
	return b.append(instruction{Op: operation, A: x.id, Bits: bits, Imm: uint64(s)}), nil
}
