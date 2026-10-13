package ir

import "github.com/moriyoshi/nyanjit/backend/op"

// DivisionSemantics says what OpDiv, OpDivSigned, OpRem and OpRemSigned do
// for a zero divisor and for signed MIN / -1; see op.DivisionSemantics.
type DivisionSemantics = op.DivisionSemantics

// ConversionSemantics says what OpFToSigned and OpFToUnsigned return for NaN
// and out-of-range values; see op.ConversionSemantics.
type ConversionSemantics = op.ConversionSemantics

const (
	DivisionZero      = op.DivisionZero
	DivisionAllOnes   = op.DivisionAllOnes
	DivisionUnchecked = op.DivisionUnchecked

	ConversionSaturate = op.ConversionSaturate
	ConversionNative   = op.ConversionNative
)
