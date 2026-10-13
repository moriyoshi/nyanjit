package ir

// BranchCondition selects the integer comparison used by a predicate.
type BranchCondition uint8

const (
	BranchEqual BranchCondition = iota
	BranchNotEqual
	BranchLess
	BranchGreaterEqual
	BranchLessEqual
	BranchGreater
	BranchBelow
	BranchAboveEqual
	BranchBelowEqual
	BranchAbove
)
