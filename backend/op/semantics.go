package op

// DivisionSemantics says what OpDiv, OpDivSigned, OpRem and OpRemSigned do
// for a zero divisor and for signed MIN / -1. An instruction's Imm holds it.
type DivisionSemantics uint8

const (
	// DivisionZero gives x/0 = 0, x%0 = x, MIN/-1 = MIN and MIN%-1 = 0, as
	// arm64 computes them.
	DivisionZero DivisionSemantics = iota
	// DivisionAllOnes gives x/0 = all ones (unsigned) or -1 (signed),
	// x%0 = x, MIN/-1 = MIN and MIN%-1 = 0, as RISC-V computes them.
	DivisionAllOnes
	// DivisionUnchecked requires a nonzero divisor and no signed MIN / -1.
	// Otherwise the result is unspecified and the instruction may trap: x86
	// raises SIGFPE.
	DivisionUnchecked

	divisionEnd
)

// Valid reports whether s is a defined DivisionSemantics.
func (s DivisionSemantics) Valid() bool { return s < divisionEnd }

// ConversionSemantics says what OpFToSigned and OpFToUnsigned return for NaN
// and out-of-range values. An instruction's Imm holds it.
type ConversionSemantics uint8

const (
	// ConversionSaturate clamps to the integer range; NaN gives 0.
	ConversionSaturate ConversionSemantics = iota
	// ConversionNative returns what the target's conversion instruction
	// gives. It never traps, but the result depends on the target.
	ConversionNative

	conversionEnd
)

// Valid reports whether s is a defined ConversionSemantics.
func (s ConversionSemantics) Valid() bool { return s < conversionEnd }
