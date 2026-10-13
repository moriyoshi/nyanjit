package compile

import "github.com/moriyoshi/nyanjit/backend/machine"

type selectionFrame struct{ id, nextArg int }
type selectionBuffers struct {
	history                                   struct{ instructions, values, scopes, scopeMap, exports, refs, paramRaw, mark, boundaries, reachable, scheduled, frames machine.BufferHistory }
	instructions                              []machineInstruction
	values                                    []machine.SelectedValue
	scopes                                    []machine.SelectedScope
	exports, refs, paramRaw, mark, boundaries []int
	reachable, scheduled                      []bool
	frames                                    []selectionFrame
	presentScopes                             map[int]bool
	scopeMapSize                              int
}

// selectionWorkspace leases selection buffers to one lowering. Selected
// programs borrow them until the next reset, rewind or release, like the
// streams of a machine.Workspace.
type selectionWorkspace struct {
	selections []*selectionBuffers
	cursor     int
	used       int
}

func (w *selectionWorkspace) reset() {
	w.release()
	w.cursor = 0
	w.used = 0
}

// rewind restarts selection leases, retaining storage for reuse.
func (w *selectionWorkspace) rewind() {
	if scratchPoison {
		for _, b := range w.selections[:w.cursor] {
			b.release(true)
		}
	}
	w.cursor = 0
}

// lease returns the next selection buffers of this cycle.
func (w *selectionWorkspace) lease() *selectionBuffers {
	i := w.cursor
	w.cursor++
	w.used = max(w.used, w.cursor)
	if i == len(w.selections) {
		w.selections = append(w.selections, new(selectionBuffers))
	}
	return w.selections[i]
}

// retainedBytes reports backing-buffer capacities and estimated scope-map
// storage, and whether any exceeds limit.
func (w *selectionWorkspace) retainedBytes(limit int) (int, bool) {
	total := 0
	big := false
	for _, b := range w.selections {
		for _, n := range b.sizes() {
			total += n
			big = big || n > limit
		}
	}
	return total, big
}

// trim discards unused leases and every buffer larger than limit.
func (w *selectionWorkspace) trim(limit int) {
	clear(w.selections[w.used:])
	w.selections = w.selections[:w.used]
	for _, b := range w.selections {
		b.trim(limit)
	}
}

// trimPointers discards only pointer-bearing buffers above limit.
func (w *selectionWorkspace) trimPointers(limit int) {
	for _, b := range w.selections {
		selectionTrim(&b.instructions, limit)
		selectionTrim(&b.values, limit)
		selectionTrim(&b.scopes, limit)
	}
}

// release invalidates borrowed results and clears references.
func (w *selectionWorkspace) release() {
	for _, b := range w.selections {
		b.release(scratchPoison)
	}
	clear(w.selections[w.used:])
	w.selections = w.selections[:w.used]
}

func (w *selectionWorkspace) decay() {
	for _, b := range w.selections {
		b.decay()
	}
}

// discards adds every retained buffer to choices, for trimTotalBudget.
func (w *selectionWorkspace) discards(choices *[]scratchDiscard) {
	for _, b := range w.selections {
		discardSelection(choices, &b.instructions)
		discardSelection(choices, &b.values)
		discardSelection(choices, &b.scopes)
		discardSelection(choices, &b.exports)
		discardSelection(choices, &b.refs)
		discardSelection(choices, &b.paramRaw)
		discardSelection(choices, &b.mark)
		discardSelection(choices, &b.boundaries)
		discardSelection(choices, &b.reachable)
		discardSelection(choices, &b.scheduled)
		discardSelection(choices, &b.frames)
		if b.presentScopes != nil {
			*choices = append(*choices, scratchDiscard{retainedScopeMapBytes(b.scopeMapSize), func() { b.presentScopes = nil; b.scopeMapSize = 0 }})
		}
	}
}

func discardSelection[T any](out *[]scratchDiscard, value *[]T) {
	if n := bufferBytes(*value); n != 0 {
		*out = append(*out, scratchDiscard{n, func() {
			if scratchPoison {
				poisonSelection(*value)
			}
			*value = nil
		}})
	}
}

func (b *selectionBuffers) sizes() [12]int {
	return [12]int{bufferBytes(b.instructions), bufferBytes(b.values), bufferBytes(b.scopes), bufferBytes(b.exports), bufferBytes(b.refs), bufferBytes(b.paramRaw), bufferBytes(b.mark), bufferBytes(b.boundaries), bufferBytes(b.reachable), bufferBytes(b.scheduled), bufferBytes(b.frames), retainedScopeMapBytes(b.scopeMapSize)}
}

func (b *selectionBuffers) release(poison bool) {
	if poison {
		for i := range b.instructions[:cap(b.instructions)] {
			b.instructions[:cap(b.instructions)][i] = machineInstruction{Op: 255, Dst: -0x5a5a, Symbol: "scratch poison"}
		}
		for i := range b.values[:cap(b.values)] {
			b.values[:cap(b.values)][i] = machine.SelectedValue{Bits: 255, Words: 255, Symbol: "scratch poison"}
		}
		for _, v := range [][]int{b.exports, b.refs, b.paramRaw, b.mark, b.boundaries} {
			for i := range v[:cap(v)] {
				v[:cap(v)][i] = -0x5a5a
			}
		}
		for _, v := range [][]bool{b.reachable, b.scheduled} {
			for i := range v[:cap(v)] {
				v[:cap(v)][i] = true
			}
		}
		for i := range b.frames[:cap(b.frames)] {
			b.frames[:cap(b.frames)][i] = selectionFrame{-0x5a5a, -0x5a5a}
		}
		clear(b.presentScopes)
		if b.presentScopes != nil {
			b.presentScopes = make(map[int]bool, b.scopeMapSize)
		}
	} else {
		clear(b.instructions)
		clear(b.values)
	}
	if poison {
		for i := range b.scopes[:cap(b.scopes)] {
			b.scopes[:cap(b.scopes)][i] = machine.SelectedScope{ID: -0x5a5a, Parent: -0x5a5a, Start: -0x5a5a, End: -0x5a5a}
		}
	} else {
		clear(b.scopes[:cap(b.scopes)])
	}
	b.instructions = b.instructions[:0]
	b.values = b.values[:0]
	b.scopes = b.scopes[:0]
	if poison {
		b.instructions = poisonReplacement(b.instructions)
		b.values = poisonReplacement(b.values)
		b.scopes = poisonReplacement(b.scopes)
		b.exports = poisonReplacement(b.exports)
		b.refs = poisonReplacement(b.refs)
		b.paramRaw = poisonReplacement(b.paramRaw)
		b.mark = poisonReplacement(b.mark)
		b.boundaries = poisonReplacement(b.boundaries)
		b.reachable = poisonReplacement(b.reachable)
		b.scheduled = poisonReplacement(b.scheduled)
		b.frames = poisonReplacement(b.frames)
	}
}

// Keep stale borrows on the poisoned old array. The new lease must clear
// equally poisoned storage, with the same capacity and demand history.
func poisonReplacement[T any](v []T) []T {
	if v == nil {
		return nil
	}
	fresh := make([]T, cap(v))
	copy(fresh, v[:cap(v)])
	return fresh[:len(v)]
}

// Conservative Swiss-table accounting, including empty slots, control bytes,
// table headers and growth slack. The map is int -> bool.
func retainedScopeMapBytes(n int) int {
	if n == 0 {
		return 0
	}
	groups := 1
	for groups*7 < n {
		groups *= 2
	}
	return 64 + (groups*(8*16+8)+64)*5/4
}

func selectionTrim[T any](v *[]T, limit int) {
	if bufferBytes(*v) > limit {
		if scratchPoison {
			poisonSelection(*v)
		}
		*v = nil
	}
}

// poisonSelection overwrites a discarded buffer so that an expired borrow
// still holding it is detected.
func poisonSelection[T any](buffer []T) {
	switch values := any(buffer[:cap(buffer)]).(type) {
	case []machineInstruction:
		for i := range values {
			values[i] = machineInstruction{Op: 255, Dst: -0x5a5a}
		}
	case []machine.SelectedValue:
		for i := range values {
			values[i] = machine.SelectedValue{Bits: 255, Words: 255}
		}
	case []machine.SelectedScope:
		for i := range values {
			values[i] = machine.SelectedScope{ID: -0x5a5a, Parent: -0x5a5a, Start: -0x5a5a, End: -0x5a5a}
		}
	case []int:
		for i := range values {
			values[i] = -0x5a5a
		}
	}
}

func (b *selectionBuffers) trim(limit int) {
	selectionTrim(&b.instructions, limit)
	selectionTrim(&b.values, limit)
	selectionTrim(&b.scopes, limit)
	selectionTrim(&b.exports, limit)
	selectionTrim(&b.refs, limit)
	selectionTrim(&b.paramRaw, limit)
	selectionTrim(&b.mark, limit)
	selectionTrim(&b.boundaries, limit)
	selectionTrim(&b.reachable, limit)
	selectionTrim(&b.scheduled, limit)
	selectionTrim(&b.frames, limit)
	if retainedScopeMapBytes(b.scopeMapSize) > limit {
		b.presentScopes = nil
		b.scopeMapSize = 0
	}
}

func selectionDecay[T any](v *[]T, h *machine.BufferHistory) {
	excess := h.FinishCycle(cap(*v))
	if bufferBytes(*v) > 4096 && excess {
		*v = nil
	}
}

func (b *selectionBuffers) decay() {
	selectionDecay(&b.instructions, &b.history.instructions)
	selectionDecay(&b.values, &b.history.values)
	selectionDecay(&b.scopes, &b.history.scopes)
	selectionDecay(&b.exports, &b.history.exports)
	selectionDecay(&b.refs, &b.history.refs)
	selectionDecay(&b.paramRaw, &b.history.paramRaw)
	selectionDecay(&b.mark, &b.history.mark)
	selectionDecay(&b.boundaries, &b.history.boundaries)
	selectionDecay(&b.reachable, &b.history.reachable)
	selectionDecay(&b.scheduled, &b.history.scheduled)
	selectionDecay(&b.frames, &b.history.frames)
	if b.history.scopeMap.FinishCycle(b.scopeMapSize) {
		b.presentScopes = nil
		b.scopeMapSize = 0
	}
}
