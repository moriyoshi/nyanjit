package builder

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/ir"
)

// Finish validates and consumes the Builder. The returned Program owns the
// instruction storage and can be compiled repeatedly. Values from this Builder
// cannot be used after Finish.
func (b *Builder) Finish() (*ir.Program, error) {
	if b == nil || b.finished {
		return nil, fmt.Errorf("Finish: builder is nil or already finished: %w", ErrInvalidFunction)
	}
	if len(b.scopes) != 1 {
		return nil, fmt.Errorf("Finish: %d nested scopes remain open: %w", len(b.scopes)-1, ErrInvalidFunction)
	}
	if len(b.code) == 0 && b.control == nil {
		return nil, fmt.Errorf("Finish: function has no instructions or return: %w", ErrInvalidFunction)
	}
	result := b.result
	var control *ir.Control
	if b.control == nil {
		if !b.returned || result < 0 || result >= len(b.code) {
			return nil, fmt.Errorf("Finish: function has no return: %w", ErrInvalidFunction)
		}
	} else {
		if b.control.active || !b.control.hasTerm || len(b.control.steps) == 0 {
			return nil, fmt.Errorf("Finish: control-flow block has no terminating branch or return: %w", ErrInvalidFunction)
		}
		for label, bound := range b.control.labels {
			if !bound {
				return nil, fmt.Errorf("Finish: label %d is unbound: %w", label, ErrInvalidFunction)
			}
		}
		control = freezeControl(b.control)
		result = -1
	}
	for _, v := range b.variables {
		if v.Immutable {
			b.code[v.Declaration].Args = append(b.code[v.Declaration].Args, v.Initial...)
		}
	}
	program := &ir.Program{
		Instructions: b.code, ScopeExits: b.leaves, Variables: b.variables,
		Control: control, Arity: b.arity, Result: result,
		ResultParts: b.resultParts,
	}
	if err := program.Validate(); err != nil {
		return nil, fmt.Errorf("Finish: program validation failed: %w: %w", ErrInvalidFunction, err)
	}
	b.code, b.leaves, b.owners, b.scopes, b.scopeStarts, b.control = nil, nil, nil, nil, nil, nil
	b.expressions = nil
	b.variables, b.captures, b.scopeTerminated = nil, nil, nil
	b.resultParts = nil
	b.arity, b.result, b.returned, b.finished = 0, -1, false, true
	return program, nil
}

func freezeControl(source *builderControl) *ir.Control {
	control := &ir.Control{
		ParamTypes:  make([][]ir.Type, len(source.paramTypes)+1),
		Labels:      append(append([]bool(nil), source.labels...), true),
		LabelScopes: append(append([]int(nil), source.labelScopes...), 0),
		Params:      append(append([]int(nil), source.params...), 0),
		ParamBits:   make([][]uint8, len(source.paramBits)+1),
		Steps:       make([]ir.ControlStep, len(source.steps)),
	}
	for i, bits := range source.paramBits {
		control.ParamBits[i] = append([]uint8(nil), bits...)
		control.ParamTypes[i] = append([]Type(nil), source.paramTypes[i]...)
	}
	memo := make(map[*predicateNode]*ir.PredicateExpr)
	var freezePredicate func(*predicateNode) *ir.PredicateExpr
	freezePredicate = func(node *predicateNode) *ir.PredicateExpr {
		if node == nil {
			return nil
		}
		if existing := memo[node]; existing != nil {
			return existing
		}
		frozen := &ir.PredicateExpr{
			Kind: ir.PredicateKind(node.kind), Condition: node.condition,
			Value: node.value, Other: node.other, Anchor: node.anchor,
			Immediate: node.immediate, WithValue: node.withValue, HasAnchor: node.hasAnchor,
		}
		memo[node] = frozen
		frozen.Left, frozen.Right = freezePredicate(node.left), freezePredicate(node.right)
		return frozen
	}
	for i, step := range source.steps {
		if step.isLabel && step.label == implicitEntryLabel {
			step.label = Label(len(source.labels))
		}
		control.Steps[i] = ir.ControlStep{
			Label: int(step.label), To: int(step.to), Other: int(step.other),
			IsLabel: step.isLabel, Term: ir.ControlTerm(step.term),
			Start: step.start, End: step.end,
			LeaveStart: step.leaveStart, LeaveEnd: step.leaveEnd,
			Result: step.result, Arity: step.arity,
			ResultParts: append([]int(nil), step.resultParts...),
			Values:      append([]int(nil), step.values...),
			Args:        append([]int(nil), step.args...),
			OtherArgs:   append([]int(nil), step.otherArgs...),
			Predicate:   freezePredicate(step.predicate),
		}
	}
	return control
}
