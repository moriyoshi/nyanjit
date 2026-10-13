package builder

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

var (
	Scalar = ir.Scalar
	Vec    = ir.Vec
	Mask   = ir.Mask
)
