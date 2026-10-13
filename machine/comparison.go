package machine

import "github.com/moriyoshi/nyanjit/ir"

// ComparisonMode specifies how the operands establish comparison flags.
// Equality comparisons need no ordering interpretation.
type ComparisonMode uint8

const (
	CompareEquality ComparisonMode = iota
	CompareSigned
	CompareUnsigned
)

// ComparisonFor returns the operand interpretation required by condition.
func ComparisonFor(condition ir.BranchCondition) ComparisonMode {
	switch condition {
	case ir.BranchLess, ir.BranchGreaterEqual, ir.BranchLessEqual, ir.BranchGreater:
		return CompareSigned
	case ir.BranchBelow, ir.BranchAboveEqual, ir.BranchBelowEqual, ir.BranchAbove:
		return CompareUnsigned
	default:
		return CompareEquality
	}
}
