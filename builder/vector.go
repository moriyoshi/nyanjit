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
func (m VMask) Type() Type { return m.value.Type() }

// Mask reconstructs a mask handle from a typed label parameter.
func (v Value) Mask() VMask {
	if v.Type().Kind != MaskKind {
		return VMask{}
	}
	return VMask{v}
}

// VLoad reads an unaligned vector with lane zero at the lowest address.
func (b *Builder) VLoad(t Type, address Value) (Value, error) {
	address, err := b.materialize(address)
	if err != nil {
		return Value{}, err
	}
	if !t.Valid() || t.Kind != VectorKind || !b.valid(address) || address.Type().Kind != ScalarKind || address.Type().LaneBits != 32 && address.Type().LaneBits != 64 {
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
	if !b.valid(address) || !b.valid(value) || value.Type().Kind != VectorKind || address.Type().Kind != ScalarKind || address.Type().LaneBits != 32 && address.Type().LaneBits != 64 {
		return fmt.Errorf("VStore: invalid vector or address: %w", ErrInvalidFunction)
	}
	b.append(instruction{Op: ir.OpVStore, Type: value.Type(), Bits: value.Type().LaneBits, A: address.id, B: value.id})
	return nil
}

// Splat repeats a matching scalar into each lane. Pure literals rematerialize in later blocks.
func (b *Builder) Splat(t Type, scalar Value) (Value, error) {
	if !b.canAppend() || !t.Valid() || t.Kind != VectorKind || !b.validExpression(scalar) || scalar.Type() != Scalar(t.LaneBits) {
		return Value{}, fmt.Errorf("Splat: vector and scalar lane widths must match: %w", ErrInvalidFunction)
	}
	return Value{builder: b, id: -1, expression: &valueExpression{op: ir.OpVSplat, typ: t, bits: t.LaneBits, left: scalar}}, nil
}
func (b *Builder) emitSplat(t Type, scalar Value) (Value, error) {
	scalar, err := b.materialize(scalar)
	if err != nil {
		return Value{}, err
	}
	return b.append(instruction{Op: ir.OpVSplat, Type: t, Bits: t.LaneBits, A: scalar.id}), nil
}

// ConstVector snapshots canonical bytes: lane zero first and little endian within each lane.
func (b *Builder) ConstVector(t Type, data []byte) (Value, error) {
	if !b.canAppend() || !t.Valid() || t.Kind != VectorKind || len(data) != int(t.Bits()/8) {
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
	if !b.valid(v) || v.Type().Kind != VectorKind || index >= v.Type().Lanes {
		return Value{}, ErrInvalidFunction
	}
	return b.append(instruction{Op: ir.OpVExtract, Type: Scalar(v.Type().LaneBits), Bits: v.Type().LaneBits, A: v.id, Imm: uint64(index)}), nil
}

// InsertLane replaces the lane at index with a width-matched scalar.
func (b *Builder) InsertLane(v Value, index uint16, scalar Value) (Value, error) {
	values, err := b.materializeValues([]Value{v, scalar})
	if err != nil {
		return Value{}, err
	}
	v, scalar = values[0], values[1]
	if !b.valid(v) || !b.valid(scalar) || v.Type().Kind != VectorKind || index >= v.Type().Lanes || scalar.Type() != Scalar(v.Type().LaneBits) {
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
	if !b.valid(x) || !b.valid(y) || x.Type().Kind != VectorKind || x.Type() != y.Type() || condition > ir.BranchAbove {
		return VMask{}, ErrInvalidFunction
	}
	return VMask{b.append(instruction{Op: ir.OpVCompare, Type: Mask(x.Type().Lanes), Bits: x.Type().LaneBits, A: x.id, B: y.id, Imm: uint64(condition)})}, nil
}

// MaskBits returns bit i for mask lane i, in the smallest scalar width that fits.
func (b *Builder) MaskBits(mask VMask) (Value, error) {
	value, err := b.materialize(mask.value)
	if err != nil {
		return Value{}, err
	}
	if !b.valid(value) || value.Type().Kind != MaskKind {
		return Value{}, ErrInvalidFunction
	}
	bits := uint8(8)
	for uint16(bits) < value.Type().Lanes {
		bits *= 2
	}
	return b.append(instruction{Op: ir.OpVMaskBits, Bits: bits, A: value.id}), nil
}

func (b *Builder) vectorBinary(operation op, x, y Value) (Value, error) {
	if !b.valid(x) || !b.valid(y) || x.Type().Kind != VectorKind || x.Type() != y.Type() {
		return Value{}, fmt.Errorf("vector arithmetic: operand Types differ or are out of scope: %w", ErrInvalidFunction)
	}
	return b.append(instruction{Op: operation, Type: x.Type(), Bits: x.Type().LaneBits, A: x.id, B: y.id}), nil
}
