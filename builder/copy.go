package builder

import (
	"maps"
	"slices"

	"github.com/moriyoshi/nyanjit/ir"
)

// The normal builder consumes its owned graph at Finish. Preserve the previous
// snapshot semantics for by-value copies by detaching before their first use.
// This also protects a sealed graph when a copy outlives the original builder.
func (b *Builder) detachCopy() {
	if b.storageOwner == nil || b.storageOwner == b {
		return
	}
	if b.seg != nil {
		// A segment Builder's storage is its stream; a copy cannot share
		// it, so the copy is finished.
		b.finished, b.storageOwner, b.seg = true, b, nil
		return
	}
	p := (&ir.Program{Instructions: b.flatCode(), ScopeExits: b.leaves, Variables: b.variables, ResultParts: b.resultParts}).Clone()
	b.code, b.leaves, b.variables, b.resultParts = p.Instructions, p.ScopeExits, p.Variables, p.ResultParts
	b.instructionSlices = slices.Clone(b.instructionSlices)
	b.parameterTypes = maps.Clone(b.parameterTypes)
	b.checkedAddresses = maps.Clone(b.checkedAddresses)
	b.codeBlocks = nil
	b.codeSize = len(b.code)
	b.values = slices.Clone(b.values)
	b.ownedStack = slices.Clone(b.ownedStack)
	b.ownedMarks = slices.Clone(b.ownedMarks)
	b.leaveWork, b.predicateWork, b.predicateIDs = nil, nil, nil
	b.scopes = slices.Clone(b.scopes)
	b.scopeStarts = slices.Clone(b.scopeStarts)
	b.scopeActive = slices.Clone(b.scopeActive)
	b.captureStarts = slices.Clone(b.captureStarts)
	b.captureEnds = slices.Clone(b.captureEnds)
	b.scopeTerminated = slices.Clone(b.scopeTerminated)
	if b.captures != nil {
		m := make(map[int]map[int]int, len(b.captures))
		for k, v := range b.captures {
			inner := make(map[int]int, len(v))
			for id, x := range v {
				inner[id] = x
			}
			m[k] = inner
		}
		b.captures = m
	}
	if source := b.control; source != nil {
		control := *source
		control.labels = slices.Clone(source.labels)
		control.privateLabels = maps.Clone(source.privateLabels)
		control.labelScopes = slices.Clone(source.labelScopes)
		control.params = slices.Clone(source.params)
		control.paramBits = cloneLists(source.paramBits)
		control.paramTypes = cloneLists(source.paramTypes)
		control.steps = slices.Clone(source.steps)
		for i := range control.steps {
			step := &control.steps[i]
			step.resultParts = slices.Clone(step.resultParts)
			step.values = slices.Clone(step.values)
			step.args = slices.Clone(step.args)
			step.otherArgs = slices.Clone(step.otherArgs)
		}
		b.control = &control
	}
	b.expressionArena = new(expressionArena)
	b.expressionWork = nil
	b.expressionSeen = nil
	b.variableInitials = nil
	b.storageOwner = b
}
func cloneLists[T any](lists [][]T) [][]T {
	result := slices.Clone(lists)
	for i := range result {
		result[i] = slices.Clone(result[i])
	}
	return result
}
