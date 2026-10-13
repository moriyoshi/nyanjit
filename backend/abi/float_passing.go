package abi

import "math/bits"

// FloatPassing marks the arguments and result of a call boundary that travel
// in floating point registers under the boundary's convention. Bit i marks
// argument i, which must be a 32- or 64-bit value; FloatResult32 or
// FloatResult64 marks a binary32 or binary64 result. The zero value passes
// every value in integer registers.
type FloatPassing uint64

const (
	FloatResult32 FloatPassing = 1 << 62
	FloatResult64 FloatPassing = 1 << 63
)

// maxFloatArguments bounds the marked arguments of one boundary (v1).
const maxFloatArguments = 8

// floatArgumentMask covers the argument marks.
const floatArgumentMask = FloatResult32 - 1

// FloatArgument returns the mark for argument i (0 ≤ i < 62).
func FloatArgument(i int) FloatPassing {
	if i < 0 || i >= 62 {
		return 0
	}
	return 1 << i
}

// Argument reports whether argument i travels in a floating point register.
func (p FloatPassing) Argument(i int) bool { return p&FloatArgument(i) != 0 }

// ResultBits returns 32 or 64 for a floating point result, else 0.
func (p FloatPassing) ResultBits() uint8 {
	switch p &^ floatArgumentMask {
	case FloatResult32:
		return 32
	case FloatResult64:
		return 64
	}
	return 0
}

// floatArguments counts the marked arguments.
func (p FloatPassing) floatArguments() int { return bits.OnesCount64(uint64(p & floatArgumentMask)) }

// validFloats reports whether p fits a boundary of arity arguments: no marks
// beyond the arguments, at most one result width and at most eight marked
// arguments.
func validFloats(p FloatPassing, arity int) bool {
	if p&FloatResult32 != 0 && p&FloatResult64 != 0 || arity < 0 {
		return false
	}
	if arity < 62 && p&floatArgumentMask>>arity != 0 {
		return false
	}
	return p.floatArguments() <= maxFloatArguments
}

// FP argument and result registers of each convention, in assignment order.
var (
	floatArgumentsAMD64C  = []string{"XMM0", "XMM1", "XMM2", "XMM3", "XMM4", "XMM5", "XMM6", "XMM7"}
	floatArgumentsARM64   = []string{"V0", "V1", "V2", "V3", "V4", "V5", "V6", "V7"}
	floatArgumentsRISCV64 = []string{"F10", "F11", "F12", "F13", "F14", "F15", "F16", "F17"}
	floatArgumentsPPC64   = []string{"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8"}
	floatArgumentsLoong64 = []string{"F0", "F1", "F2", "F3", "F4", "F5", "F6", "F7"}
)

// floatConvention returns the FP argument registers and result register of a
// register convention, and whether a float argument consumes the positional
// integer register it would otherwise use (ppc64le ELFv2). 386 has none.
func floatConvention(architecture string, flavor Flavor) (arguments []string, result string, positional bool) {
	switch architecture {
	case "amd64":
		return floatArgumentsAMD64C, "XMM0", false
	case "arm64":
		return floatArgumentsARM64, "V0", false
	case "riscv64":
		return floatArgumentsRISCV64, "F10", false
	case "ppc64le":
		return floatArgumentsPPC64, "F1", flavor == C
	case "loong64":
		return floatArgumentsLoong64, "F0", false
	}
	return nil, "", false
}

// floatRegisters reassigns integer argument registers to a float passing:
// integer arguments take the convention's integer registers in order (or
// their positional one), and marked arguments the next FP register.
func floatRegisters(architecture string, flavor Flavor, integer []string, floats FloatPassing) (arguments []string, result string) {
	fp, result, positional := floatConvention(architecture, flavor)
	arguments = make([]string, len(integer))
	nextGP, nextFP := 0, 0
	for i := range integer {
		switch {
		case floats.Argument(i):
			arguments[i] = fp[nextFP]
			nextFP++
		case positional:
			arguments[i] = integer[i]
		default:
			arguments[i] = integer[nextGP]
		}
		if !floats.Argument(i) || positional {
			nextGP++
		}
	}
	if floats.ResultBits() == 0 {
		result = ""
	}
	return arguments, result
}
