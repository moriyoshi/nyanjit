package builder

import (
	"fmt"
	"math"

	"github.com/moriyoshi/nyanjit/ir"
)

// Float32 creates a constant containing the IEEE 754 bits of value.
// Floating values use the ordinary 32-bit Value and integer-register ABI;
// this is a bit-pattern interface, not a Go float argument or return ABI.
func (b *Builder) Float32(value float32) (Value, error) {
	return b.ConstBits(32, uint64(math.Float32bits(value)))
}

// Float64 creates a constant containing the IEEE 754 bits of value.
func (b *Builder) Float64(value float64) (Value, error) {
	return b.ConstBits(64, math.Float64bits(value))
}

// FAdd adds two IEEE 754 bit patterns of equal width (32 or 64 bits).
func (b *Builder) FAdd(x, y Value) (Value, error) { return b.floatBinary(ir.OpFAdd, x, y) }

// FSub subtracts two IEEE 754 bit patterns of equal width.
func (b *Builder) FSub(x, y Value) (Value, error) { return b.floatBinary(ir.OpFSub, x, y) }

// FMul multiplies two IEEE 754 bit patterns of equal width.
func (b *Builder) FMul(x, y Value) (Value, error) { return b.floatBinary(ir.OpFMul, x, y) }

// FDiv divides two IEEE 754 bit patterns of equal width.
func (b *Builder) FDiv(x, y Value) (Value, error) { return b.floatBinary(ir.OpFDiv, x, y) }

// FSqrt computes the square root of an IEEE 754 bit pattern (32 or 64 bits).
// It preserves signed zero and returns NaN for negative nonzero inputs.
// Arithmetic follows the target's current floating point environment.
func (b *Builder) FSqrt(x Value) (Value, error) {
	if b == nil || x.Type().Kind != ir.ScalarKind || !b.validExpression(x) || x.Type().LaneBits != 32 && x.Type().LaneBits != 64 {
		return Value{}, fmt.Errorf("FSqrt: operand must be in scope and have 32 or 64 bits: %w", ErrInvalidFunction)
	}
	if x.expression != nil {
		return x.FSqrt(), nil
	}
	return b.append(instruction{Op: ir.OpFSqrt, A: x.id, Bits: x.Type().LaneBits}), nil
}

// FConvert converts between binary32 and binary64 floating point bit patterns.
// Narrowing follows the target's current floating point rounding mode.
func (b *Builder) FConvert(x Value, bits uint8) (Value, error) {
	if b == nil || x.Type().Kind != ir.ScalarKind || !b.validExpression(x) || x.Type().LaneBits != 32 && x.Type().LaneBits != 64 || bits != 32 && bits != 64 {
		return Value{}, ErrInvalidFunction
	}
	if bits == x.Type().LaneBits {
		return x, nil
	}
	if x.expression != nil {
		return x.FConvert(bits), nil
	}
	return b.append(instruction{Op: ir.OpFConvert, A: x.id, Bits: bits}), nil
}

// FMA computes x*y+addend with one rounding step. Compilation requires a
// hardware FMA instruction. Deferred FMul/FAdd expressions are contracted
// automatically when the target has one.
func (b *Builder) FMA(x, y, addend Value) (Value, error) {
	if b == nil || x.Type().Kind != ir.ScalarKind || y.Type().Kind != ir.ScalarKind || addend.Type().Kind != ir.ScalarKind || !b.validExpression(x) || !b.validExpression(y) || !b.validExpression(addend) ||
		(x.Type().LaneBits != 32 && x.Type().LaneBits != 64) || x.Type().LaneBits != y.Type().LaneBits || x.Type().LaneBits != addend.Type().LaneBits {
		return Value{}, fmt.Errorf("FMA: operands must be in scope and have matching 32- or 64-bit widths (values %d, %d, %d): %w",
			x.id, y.id, addend.id, ErrInvalidFunction)
	}
	if x.expression != nil || y.expression != nil || addend.expression != nil {
		return x.FMA(y, addend), nil
	}
	return b.append(instruction{Op: ir.OpFMA, A: x.id, B: y.id, C: addend.id, Bits: x.Type().LaneBits}), nil
}

func floatOperationName(operation op) string {
	switch operation {
	case ir.OpFAdd:
		return "FAdd"
	case ir.OpFSub:
		return "FSub"
	case ir.OpFMul:
		return "FMul"
	case ir.OpFDiv:
		return "FDiv"
	}
	return ""
}

func (b *Builder) floatBinary(operation op, x, y Value) (Value, error) {
	if b == nil || x.Type().Kind != ir.ScalarKind || y.Type().Kind != ir.ScalarKind || !b.validExpression(x) || !b.validExpression(y) ||
		(x.Type().LaneBits != 32 && x.Type().LaneBits != 64) || x.Type().LaneBits != y.Type().LaneBits {
		return Value{}, fmt.Errorf("%s: operands must be in scope and have matching 32- or 64-bit widths (values %d and %d have %d and %d bits): %w",
			floatOperationName(operation), x.id, y.id, x.Type().LaneBits, y.Type().LaneBits, ErrInvalidFunction)
	}
	if x.expression != nil || y.expression != nil {
		return expressionBinary(operation, x, y), nil
	}
	return b.append(instruction{Op: operation, A: x.id, B: y.id, Bits: x.Type().LaneBits}), nil
}

// FCompare compares equal-width (32- or 64-bit) floating point bit patterns
// x and y by condition. It returns 1 when the condition holds and 0
// otherwise, at the operands' width. Status flags may change; whether a quiet
// NaN raises invalid depends on the target.
func (b *Builder) FCompare(x Value, condition ir.FloatCondition, y Value) (Value, error) {
	x, y, err := b.scalarPair(ir.OpFCompare, x, y)
	if err != nil {
		return Value{}, err
	}
	if bits := x.Type().LaneBits; bits != 32 && bits != 64 || condition > ir.FloatUnorderedEqual {
		return Value{}, fmt.Errorf("FCompare: operands must have 32 or 64 bits and condition %d must be defined: %w", condition, ErrInvalidFunction)
	}
	return b.append(instruction{Op: ir.OpFCompare, A: x.id, B: y.id, Imm: uint64(condition), Bits: x.Type().LaneBits}), nil
}

// FComparePredicate is the branch predicate of FCompare(x, condition, y).
func (b *Builder) FComparePredicate(x Value, condition ir.FloatCondition, y Value) (Predicate, error) {
	value, err := b.FCompare(x, condition, y)
	if err != nil {
		return Predicate{}, err
	}
	return b.CompareImmediate(value, ir.BranchNotEqual, 0)
}
