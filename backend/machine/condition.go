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
	// w7 cmp: BranchOverflow and BranchNotOverflow test signed overflow of
	// the last arithmetic (x86 OF, arm64 V).
	BranchOverflow
	BranchNotOverflow
)
