package builder

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/moriyoshi/nyanjit/ir"
)

// Finish validates and consumes the Builder, returning an immutable snapshot
// with reusable source analyses. Clone the result to inspect or edit raw IR.
// Values from this Builder cannot be used after Finish. An incomplete or
// invalid function (open scopes, unbound labels, no return) returns an error
// wrapping ErrInvalidFunction. On a Builder from NewSegmentBuilder, Finish
// returns an error wrapping ErrInvalidFunction; use End.
func (b *Builder) Finish() (*ir.SealedProgram, error) {
	if b == nil || b.finished {
		return nil, fmt.Errorf("Finish: builder is nil or already finished: %w", ErrInvalidFunction)
	}
	if b.seg != nil {
		if finish := b.seg.testFinish; finish != nil {
			return finish(b.End())
		}
		return nil, fmt.Errorf("Finish: builder is from NewSegmentBuilder; use End: %w", ErrInvalidFunction)
	}
	b.detachCopy()
	if len(b.scopes) != 1 {
		return nil, fmt.Errorf("Finish: %d nested scopes remain open: %w", len(b.scopes)-1, ErrInvalidFunction)
	}
	if b.codeLen() == 0 && b.control == nil {
		return nil, fmt.Errorf("Finish: function has no instructions or return: %w", ErrInvalidFunction)
	}
	result := b.result
	var control *ir.Control
	if b.control == nil {
		if !b.returned || result < 0 || result >= b.codeLen() {
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
	if b.splitErr != nil {
		return nil, fmt.Errorf("Finish: scope entry validation failed: %w: %w", ErrInvalidFunction, b.splitErr)
	}
	for _, v := range b.variables {
		if v.Immutable {
			b.instructionSlices = append(b.instructionSlices, v.Declaration)
			b.appendCaptureArgs(v.Declaration, v.Initial)
		}
	}
	program := &ir.Program{
		Instructions: b.finishCode(), ScopeExits: b.leaves, Variables: b.variables,
		Control: control, Arity: b.arity, Result: result,
		ResultParts: b.resultParts,
	}
	if len(b.parameterTypes) != 0 {
		program.ParameterTypes = make([]reflect.Type, b.arity)
		for index, typ := range b.parameterTypes {
			program.ParameterTypes[index] = typ
		}
	}
	slices.Sort(b.instructionSlices)
	b.instructionSlices = slices.Compact(b.instructionSlices)
	compactProgram(program, b.instructionSlices)
	sealed, err := ir.SealOwned(program)
	if err != nil {
		return nil, fmt.Errorf("Finish: program validation failed: %w: %w", ErrInvalidFunction, err)
	}
	b.instructionSlices = nil
	b.codeBlocks = nil
	b.codeSize = 0
	b.code, b.leaves, b.values, b.scopes, b.scopeStarts, b.control = nil, nil, nil, nil, nil, nil
	b.scopeActive, b.ownedStack, b.ownedMarks, b.captureStarts, b.captureEnds = nil, nil, nil, nil, nil
	if arena := b.expressionArena; arena != nil {
		arena.mu.Lock()
		arena.slab, arena.predicates = nil, nil
		arena.mu.Unlock()
	}
	b.expressionWork = nil
	b.expressionSeen = nil
	b.predicateWork, b.predicateIDs, b.leaveWork = nil, nil, nil
	b.variables, b.captures, b.scopeTerminated = nil, nil, nil
	b.variableInitials = nil
	b.resultParts = nil
	b.arity, b.result, b.returned, b.finished = 0, -1, false, true
	// Caller data were detached when appended, so the sealed program owns all
	// of its storage.
	return sealed, nil
}

func freezeControl(source *builderControl) *ir.Control {
	control := &ir.Control{
		ParamTypes:  append(source.paramTypes, nil),
		Labels:      append(source.labels, true),
		LabelScopes: append(source.labelScopes, 0),
		Params:      append(source.params, 0),
		ParamBits:   append(source.paramBits, nil),
		Steps:       make([]ir.ControlStep, len(source.steps)),
	}
	memo := make(map[*predicateNode]*ir.PredicateExpr)
	freezePredicate := func(root *predicateNode) *ir.PredicateExpr {
		var work []*predicateNode
		copyRef := func(node *predicateNode) *ir.PredicateExpr {
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
			work = append(work, node)
			return frozen
		}
		result := copyRef(root)
		for len(work) != 0 {
			node := work[len(work)-1]
			work = work[:len(work)-1]
			memo[node].Left, memo[node].Right = copyRef(node.left), copyRef(node.right)
		}
		return result
	}
	for i, step := range source.steps {
		if step.isLabel && step.label == implicitEntryLabel {
			step.label = ir.Label(len(source.labels))
		}
		control.Steps[i] = ir.ControlStep{
			Label: int(step.label), To: int(step.to), Other: int(step.other),
			IsLabel: step.isLabel, Term: ir.ControlTerm(step.term),
			Start: step.start, End: step.end,
			LeaveStart: step.leaveStart, LeaveEnd: step.leaveEnd,
			Result: step.result, Arity: step.arity,
			ResultParts: step.resultParts,
			Values:      step.values,
			Args:        step.args,
			OtherArgs:   step.otherArgs,
			Predicate:   freezePredicate(step.predicate),
		}
	}
	return control
}
