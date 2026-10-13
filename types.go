package nyanjit

import "github.com/moriyoshi/nyanjit/ir"

// Type describes a scalar, lane vector or boolean mask.
type Type = ir.Type

// Kind identifies a logical value class.
type Kind = ir.Kind

const (
	InvalidKind = ir.InvalidKind
	ScalarKind  = ir.ScalarKind
	VectorKind  = ir.VectorKind
	MaskKind    = ir.MaskKind
)

// Scalar describes a scalar bit pattern.
func Scalar(bits uint8) Type { return ir.Scalar(bits) }

// Vec describes a fixed vector shape.
func Vec(lanes uint16, bits uint8) Type { return ir.Vec(lanes, bits) }

// Mask describes one boolean per lane.
func Mask(lanes uint16) Type { return ir.Mask(lanes) }
