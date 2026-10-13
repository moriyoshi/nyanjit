package builder

import (
	"fmt"
	"github.com/moriyoshi/nyanjit/ir"
)

// VMask is a logical boolean per vector lane; its physical representation is private.
type VMask struct{ value Value }

// Value returns the typed handle for scope exports and label arguments.
func (m VMask) Value() Value { return m.value }

// Type returns the mask's logical shape.
func (m VMask) Type() ir.Type { return m.value.Type() }

// Mask reconstructs a mask handle from a mask-typed Value, such as a Var's
// snapshot.
func (v Value) Mask() VMask {
	if v.Type().Kind != ir.MaskKind {
		return VMask{}
	}
	return VMask{v}
}

// VLoad reads an unaligned vector with lane zero at the lowest address.
func (b *Builder) VLoad(t ir.Type, address Value) (Value, error) {
	address, err := b.materialize(address)
	if err != nil {
		return Value{}, err
	}
	if !t.Valid() || t.Kind != ir.VectorKind || !b.valid(address) || address.Type().Kind != ir.ScalarKind || address.Type().LaneBits != 32 && address.Type().LaneBits != 64 {
		return Value{}, fmt.Errorf("VLoad: invalid vector shape or address: %w", ErrInvalidFunction)
	}
	return b.append(instruction{Op: ir.OpVLoad, Type: t, Bits: t.LaneBits, A: address.id}), nil
}

// VStore writes exactly the vector's bytes, permitting an unaligned address.
func (b *Builder) VStore(address, value Value) error {
	values, err := b.materializeValues([]Value{address, value})
	if err != nil {
		return err
	}
	address, value = values[0], values[1]
	if !b.valid(address) || !b.valid(value) || value.Type().Kind != ir.VectorKind || address.Type().Kind != ir.ScalarKind || address.Type().LaneBits != 32 && address.Type().LaneBits != 64 {
		return fmt.Errorf("VStore: invalid vector or address: %w", ErrInvalidFunction)
	}
	b.append(instruction{Op: ir.OpVStore, Type: value.Type(), Bits: value.Type().LaneBits, A: address.id, B: value.id})
	return nil
}

// Splat repeats a matching scalar into each lane. Pure literals rematerialize in later blocks.
func (b *Builder) Splat(t ir.Type, scalar Value) (Value, error) {
	if !b.canAppend() || !t.Valid() || t.Kind != ir.VectorKind || !b.validExpression(scalar) || scalar.Type() != ir.Scalar(t.LaneBits) {
		return Value{}, fmt.Errorf("Splat: vector and scalar lane widths must match: %w", ErrInvalidFunction)
	}
	return Value{builder: b, id: -1, expression: b.newExpression(valueExpression{op: ir.OpVSplat, typ: t, bits: t.LaneBits, left: scalar})}, nil
}
func (b *Builder) emitSplat(t ir.Type, scalar Value) (Value, error) {
	scalar, err := b.materialize(scalar)
	if err != nil {
		return Value{}, err
	}
	return b.append(instruction{Op: ir.OpVSplat, Type: t, Bits: t.LaneBits, A: scalar.id}), nil
}

// ConstVector snapshots canonical bytes: lane zero first and little endian within each lane.
func (b *Builder) ConstVector(t ir.Type, data []byte) (Value, error) {
	if !b.canAppend() || !t.Valid() || t.Kind != ir.VectorKind || len(data) != int(t.Bits()/8) {
		return Value{}, fmt.Errorf("ConstVector: need exactly the vector's bytes: %w", ErrInvalidFunction)
	}
	return b.append(instruction{Op: ir.OpVConst, Type: t, Bits: t.LaneBits, Data: append([]byte(nil), data...)}), nil
}

// ExtractLane returns the scalar at the constant lane index.
func (b *Builder) ExtractLane(v Value, index uint16) (Value, error) {
	v, err := b.materialize(v)
	if err != nil {
		return Value{}, err
	}
	if !b.valid(v) || v.Type().Kind != ir.VectorKind || index >= v.Type().Lanes {
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: ir.OpVExtract, Type: ir.Scalar(v.Type().LaneBits), Bits: v.Type().LaneBits, A: v.id, Imm: uint64(index)}), nil
}

// InsertLane replaces the lane at index with a width-matched scalar.
func (b *Builder) InsertLane(v Value, index uint16, scalar Value) (Value, error) {
	values, err := b.materializeValues([]Value{v, scalar})
	if err != nil {
		return Value{}, err
	}
	v, scalar = values[0], values[1]
	if !b.valid(v) || !b.valid(scalar) || v.Type().Kind != ir.VectorKind || index >= v.Type().Lanes || scalar.Type() != ir.Scalar(v.Type().LaneBits) {
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: ir.OpVInsert, Type: v.Type(), Bits: v.Type().LaneBits, A: v.id, B: scalar.id, Imm: uint64(index)}), nil
}

// VCompare compares matching integer vectors lane by lane.
func (b *Builder) VCompare(x Value, condition ir.BranchCondition, y Value) (VMask, error) {
	values, err := b.materializeValues([]Value{x, y})
	if err != nil {
		return VMask{}, err
	}
	x, y = values[0], values[1]
	if !b.valid(x) || !b.valid(y) || x.Type().Kind != ir.VectorKind || x.Type() != y.Type() || condition > ir.BranchAbove {
		return VMask{}, ErrInvalidFunction
	}
	return VMask{b.append(instruction{Op: ir.OpVCompare, Type: ir.Mask(x.Type().Lanes), Bits: x.Type().LaneBits, A: x.id, B: y.id, Imm: uint64(condition)})}, nil
}

// MaskBits returns bit i for mask lane i, in the smallest scalar width that fits.
func (b *Builder) MaskBits(mask VMask) (Value, error) {
	value, err := b.materialize(mask.value)
	if err != nil {
		return Value{}, err
	}
	if !b.valid(value) || value.Type().Kind != ir.MaskKind {
		return Value{}, ErrInvalidFunction
	}
	bits := uint8(8)
	for uint16(bits) < value.Type().Lanes {
		bits *= 2
	}
	return b.append(instruction{Op: ir.OpVMaskBits, Bits: bits, A: value.id}), nil
}

func (b *Builder) vectorBinary(operation op, x, y Value) (Value, error) {
	if !b.valid(x) || !b.valid(y) || x.Type().Kind != ir.VectorKind || x.Type() != y.Type() {
		return Value{}, fmt.Errorf("vector arithmetic: operand Types differ or are out of scope: %w", ErrInvalidFunction)
	}
	return b.append(instruction{Op: operation, Type: x.Type(), Bits: x.Type().LaneBits, A: x.id, B: y.id}), nil
}

// CarrylessMul multiplies selected 64-bit polynomials without carry or reduction.
// x and y must have the same vector Type with an even number of 64-bit lanes.
// xLane and yLane select lane 0 or 1 within every 128-bit chunk. The result has
// the same Type; each output chunk contains the product's low and high words.
func (b *Builder) CarrylessMul(x, y Value, xLane, yLane uint16) (Value, error) {
	if !b.validExpression(x) || !b.validExpression(y) {
		return Value{}, fmt.Errorf("CarrylessMul: operands must belong to this builder and remain in scope: %w", ErrInvalidFunction)
	}
	result := x.CarrylessMul(y, xLane, yLane)
	if !result.Type().Valid() {
		return Value{}, fmt.Errorf("CarrylessMul: values must have matching even-count 64-bit vector lanes and selectors must be 0 or 1 (got types %+v/%+v, selectors %d/%d): %w", x.Type(), y.Type(), xLane, yLane, ErrInvalidFunction)
	}
	return result, nil
}

// CarrylessMul composes a polynomial product of selected 64-bit lanes in every
// 128-bit chunk. Each output chunk contains low then high product words.
func (v Value) CarrylessMul(other Value, vLane, otherLane uint16) Value {
	t := v.Type()
	if v.builder == nil || v.builder != other.builder || !t.Valid() || t.Kind != ir.VectorKind || t.LaneBits != 64 || t.Lanes%2 != 0 || t != other.Type() || vLane > 1 || otherLane > 1 {
		return Value{}
	}
	return Value{builder: v.builder, id: -1, expression: v.builder.newExpression(valueExpression{op: ir.OpVCarrylessMul, typ: t, bits: 64, left: v, right: other, imm: uint64(vLane) | uint64(otherLane)<<1})}
}
