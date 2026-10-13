package machine

import "github.com/moriyoshi/nyanjit/backend/op"

// ComparisonMode specifies how the operands establish comparison flags.
// Equality comparisons need no ordering interpretation.
type ComparisonMode uint8

const (
	CompareEquality ComparisonMode = iota
	CompareSigned
	CompareUnsigned
)

// ComparisonFor returns the operand interpretation required by condition.
func ComparisonFor(condition op.BranchCondition) ComparisonMode {
	switch condition {
	case op.BranchLess, op.BranchGreaterEqual, op.BranchLessEqual, op.BranchGreater:
		return CompareSigned
	case op.BranchBelow, op.BranchAboveEqual, op.BranchBelowEqual, op.BranchAbove:
		return CompareUnsigned
	default:
		return CompareEquality
	}
}
