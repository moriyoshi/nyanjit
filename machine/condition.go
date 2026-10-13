package machine

import "github.com/moriyoshi/nyanjit/ir"

// Flag branches are allocated-machine conditions, never source comparisons.
const (
	BranchSign ir.BranchCondition = 10 + iota
	BranchNotSign
	BranchCarry
	BranchNotCarry
	BranchCarry32
	BranchNotCarry32
)
