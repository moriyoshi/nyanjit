package compile

import "github.com/moriyoshi/nyanjit/ir"

// flagBranchPlan is a comparison that the surviving arithmetic flags can decide.
// Every matching conditional edge precedes the final unconditional edge.
type flagBranchPlan struct {
	yes   []BranchCondition
	no    []BranchCondition
	valid bool
}

func flagPlanForPredicate(node *ir.PredicateExpr, flags flagState, architecture string) flagBranchPlan {
	if node == nil || node.Kind != ir.PredicateCompare {
		return flagBranchPlan{}
	}
	if !node.WithValue && node.Immediate == 0 {
		zero := flags.Zero.MatchesValue(node.Value)
		sign := flags.Sign.MatchesValue(node.Value)
		switch node.Condition {
		case BranchEqual:
			if zero {
				return flagBranchPlan{yes: []BranchCondition{BranchEqual}, valid: true}
			}
		case BranchNotEqual:
			if zero {
				return flagBranchPlan{yes: []BranchCondition{BranchNotEqual}, valid: true}
			}
		case BranchLess:
			if sign {
				return flagBranchPlan{yes: []BranchCondition{branchSign}, valid: true}
			}
		case BranchGreaterEqual:
			if sign {
				return flagBranchPlan{yes: []BranchCondition{branchNotSign}, valid: true}
			}
		case BranchLessEqual:
			if sign && zero {
				return flagBranchPlan{yes: []BranchCondition{branchSign, BranchEqual}, valid: true}
			}
		case BranchGreater:
			if sign && zero {
				return flagBranchPlan{no: []BranchCondition{branchSign, BranchEqual}, valid: true}
			}
		}
	}
	carry := flags.Carry
	if !carry.Valid || node.Condition != BranchBelow && node.Condition != BranchAboveEqual {
		return flagBranchPlan{}
	}
	if node.HasAnchor && !carry.MatchesValue(node.Anchor) {
		return flagBranchPlan{}
	}
	wantCarry := node.Condition == BranchBelow
	match := false
	switch carry.Operation {
	case opSub:
		if carry.ImmediateRight {
			match = node.Value == carry.Left && ((!node.WithValue && uint64(node.Immediate) == carry.Immediate) ||
				node.WithValue && carry.Right >= 0 && node.Other == carry.Right)
		} else {
			match = node.WithValue && node.Value == carry.Left && node.Other == carry.Right
		}
		if carry.NoBorrow {
			wantCarry = !wantCarry
		}
	case opAdd:
		match = node.WithValue && carry.MatchesValue(node.Value) &&
			(node.Other == carry.Left || node.Other == carry.Right)
	}
	if !match {
		return flagBranchPlan{}
	}
	if carry.ConditionValid {
		condition := carry.Condition
		if !wantCarry {
			condition ^= 1
		}
		return flagBranchPlan{yes: []BranchCondition{condition}, valid: true}
	}
	condition := BranchBelow
	if architecture == "arm64" {
		condition = BranchAboveEqual
	}
	if !wantCarry {
		if condition == BranchBelow {
			condition = BranchAboveEqual
		} else {
			condition = BranchBelow
		}
	}
	return flagBranchPlan{yes: []BranchCondition{condition}, valid: true}
}

func predicateFullyDecidedByFlags(node *ir.PredicateExpr, flags flagState, architecture string) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case ir.PredicateCompare:
		return flagPlanForPredicate(node, flags, architecture).valid
	case ir.PredicateNot:
		return predicateFullyDecidedByFlags(node.Left, flags, architecture)
	case ir.PredicateAnd, ir.PredicateOr:
		return predicateFullyDecidedByFlags(node.Left, flags, architecture) &&
			predicateFullyDecidedByFlags(node.Right, flags, architecture)
	default:
		return false
	}
}
