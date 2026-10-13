package ir

import "github.com/moriyoshi/nyanjit/backend/op"

// BranchCondition selects the integer comparison used by a predicate. It is
// op.BranchCondition.
type BranchCondition = op.BranchCondition

// The conditions of op.BranchCondition, re-exported for IR code.
const (
	BranchEqual        = op.BranchEqual
	BranchNotEqual     = op.BranchNotEqual
	BranchLess         = op.BranchLess
	BranchGreaterEqual = op.BranchGreaterEqual
	BranchLessEqual    = op.BranchLessEqual
	BranchGreater      = op.BranchGreater
	BranchBelow        = op.BranchBelow
	BranchAboveEqual   = op.BranchAboveEqual
	BranchBelowEqual   = op.BranchBelowEqual
	BranchAbove        = op.BranchAbove
)

// FloatCondition selects the IEEE 754 comparison of OpFCompare. It is
// op.FloatCondition.
type FloatCondition = op.FloatCondition

// The conditions of op.FloatCondition, re-exported for IR code.
const (
	FloatEqual                 = op.FloatEqual
	FloatNotEqual              = op.FloatNotEqual
	FloatLess                  = op.FloatLess
	FloatUnorderedGreaterEqual = op.FloatUnorderedGreaterEqual
	FloatLessEqual             = op.FloatLessEqual
	FloatUnorderedGreater      = op.FloatUnorderedGreater
	FloatGreater               = op.FloatGreater
	FloatUnorderedLessEqual    = op.FloatUnorderedLessEqual
	FloatGreaterEqual          = op.FloatGreaterEqual
	FloatUnorderedLess         = op.FloatUnorderedLess
	FloatOrdered               = op.FloatOrdered
	FloatUnordered             = op.FloatUnordered
	FloatOrderedNotEqual       = op.FloatOrderedNotEqual
	FloatUnorderedEqual        = op.FloatUnorderedEqual
)
