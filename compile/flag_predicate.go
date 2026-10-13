package compile

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// flagBranchPlan is a comparison that the surviving arithmetic flags can decide.
// Every matching conditional edge precedes the final unconditional edge.
type flagBranchPlan struct {
	yes   []ir.BranchCondition
	no    []ir.BranchCondition
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
		case ir.BranchEqual:
			if zero {
				return flagBranchPlan{yes: []ir.BranchCondition{ir.BranchEqual}, valid: true}
			}
		case ir.BranchNotEqual:
			if zero {
				return flagBranchPlan{yes: []ir.BranchCondition{ir.BranchNotEqual}, valid: true}
			}
		case ir.BranchLess:
			if sign {
				return flagBranchPlan{yes: []ir.BranchCondition{branchSign}, valid: true}
			}
		case ir.BranchGreaterEqual:
			if sign {
				return flagBranchPlan{yes: []ir.BranchCondition{branchNotSign}, valid: true}
			}
		case ir.BranchLessEqual:
			if sign && zero {
				return flagBranchPlan{yes: []ir.BranchCondition{branchSign, ir.BranchEqual}, valid: true}
			}
		case ir.BranchGreater:
			if sign && zero {
				return flagBranchPlan{no: []ir.BranchCondition{branchSign, ir.BranchEqual}, valid: true}
			}
		}
	}
	carry := flags.Carry
	if !carry.Valid || node.Condition != ir.BranchBelow && node.Condition != ir.BranchAboveEqual {
		return flagBranchPlan{}
	}
	if node.HasAnchor && !carry.MatchesValue(node.Anchor) {
		return flagBranchPlan{}
	}
	wantCarry := node.Condition == ir.BranchBelow
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
		return flagBranchPlan{yes: []ir.BranchCondition{condition}, valid: true}
	}
	condition := ir.BranchBelow
	if architecture == "arm64" {
		condition = ir.BranchAboveEqual
	}
	if !wantCarry {
		if condition == ir.BranchBelow {
			condition = ir.BranchAboveEqual
		} else {
			condition = ir.BranchBelow
		}
	}
	return flagBranchPlan{yes: []ir.BranchCondition{condition}, valid: true}
}

func predicateFullyDecidedByFlags(node *ir.PredicateExpr, flags flagState, architecture string) bool {
	if node == nil {
		return false
	}
	for comparison, err := range predicateComparisons(node) {
		if err != nil {
			return false
		}
		if !flagPlanForPredicate(comparison, flags, architecture).valid {
			return false
		}
	}
	return true
}

// flagOnlyBranch reports whether the export-free allocation tried for a
// branch block can emit its predicate. emitComparison decides comparisons
// from the flags only outside hardened code; hardened code compares
// explicitly, and the trial allocation keeps only the one-word block result.
func flagOnlyBranch(node *ir.PredicateExpr, program machineProgram, flags flagState, architecture string, harden bool) bool {
	if !predicateFullyDecidedByFlags(node, flags, architecture) {
		return false
	}
	if !harden {
		return true
	}
	result := program.Result
	if result < 0 || result >= len(program.Values) || program.Values[result].Words != 1 {
		return false
	}
	for comparison, err := range predicateComparisons(node) {
		if err != nil || comparison.Value != result || comparison.WithValue && comparison.Other != result {
			return false
		}
	}
	return true
}

// registerImageFlags keeps a sign fact only for values whose register image
// it describes. Predicates compare narrow values zero-extended at the register
// width, so the sign of narrower arithmetic decides them only for a value
// sign-extended to the full register; zero and carry facts are unaffected.
func registerImageFlags(flags flagState, program machineProgram, registerBits uint8) flagState {
	sign := flags.Sign
	if !sign.Valid || sign.Width == 0 || sign.Width == registerBits {
		return flags
	}
	// One pass classifies each value by its first definition.
	words := (len(program.Values) + 63) / 64
	defined, extended := make([]uint64, words), make([]uint64, words)
	for _, ins := range program.Instructions {
		value := ins.Dst
		if value < 0 || value >= len(program.Values) || defined[value/64]&(1<<(value%64)) != 0 {
			continue
		}
		defined[value/64] |= 1 << (value % 64)
		if ins.Op == machine.SelectedSignExtend {
			extended[value/64] |= 1 << (value % 64)
		}
	}
	signExtended := func(value int) bool {
		return value >= 0 && value < len(program.Values) && extended[value/64]&(1<<(value%64)) != 0
	}
	var aliases []int
	for _, value := range sign.Aliases {
		if signExtended(value) {
			aliases = append(aliases, value)
		}
	}
	switch {
	case signExtended(sign.Value):
	case len(aliases) != 0:
		sign.Value, aliases = aliases[0], aliases[1:]
	default:
		flags.Sign = machine.FlagFact{}
		return flags
	}
	sign.Aliases = aliases
	flags.Sign = sign
	return flags
}
