package machine

import "github.com/moriyoshi/nyanjit/backend/op"

// Flag branches are allocated-machine conditions, never source comparisons.
const (
	BranchSign op.BranchCondition = 10 + iota
	BranchNotSign
	BranchCarry
	BranchNotCarry
	BranchCarry32
	BranchNotCarry32
)
