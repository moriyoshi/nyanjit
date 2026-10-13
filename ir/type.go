package ir

import "github.com/moriyoshi/nyanjit/backend/abi"

// Kind distinguishes scalar, vector and mask shapes.
type Kind = abi.ValueKind

// Type is the shared logical shape used by IR, ABI metadata and reports.
type Type = abi.ValueType

// Value kinds. A scalar has one lane of 8 to 128 bits, a vector has at least
// two lanes of 8 to 64 bits and at most 512 bits in all, and a mask has 2 to
// 64 one-bit lanes with a private physical representation.
const (
	InvalidKind = abi.InvalidKind
	ScalarKind  = abi.ScalarKind
	VectorKind  = abi.VectorKind
	MaskKind    = abi.MaskKind
)

// Scalar describes an integer or floating bit pattern of the given width.
func Scalar(bits uint8) Type {
	t := Type{Kind: ScalarKind, Lanes: 1, LaneBits: bits}
	if !t.Valid() {
		return Type{}
	}
	return t
}

// Vec describes a fixed vector whose lane zero is first in memory.
func Vec(lanes uint16, bits uint8) Type {
	t := Type{Kind: VectorKind, Lanes: lanes, LaneBits: bits}
	if !t.Valid() {
		return Type{}
	}
	return t
}

// Mask describes one boolean per lane.
func Mask(lanes uint16) Type {
	t := Type{Kind: MaskKind, Lanes: lanes, LaneBits: 1}
	if !t.Valid() {
		return Type{}
	}
	return t
}

// ValueType returns an instruction's explicit type or its legacy scalar type.
func (i Instruction) ValueType() Type { return instructionValueType(i.Type, i.Bits) }

// Keep the public value receiver small enough to inline without copying the
// full instruction at each operand-type check.
func instructionValueType(t Type, bits uint8) Type {
	if t != (Type{}) {
		return t
	}
	if bits == 0 {
		bits = 64
	}
	return Scalar(bits)
}
