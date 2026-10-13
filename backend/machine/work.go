package machine

import "unsafe"

// Workspace owns reusable stream buffers. The zero value is ready
// to use. Results are borrowed until Reset, Rewind or Release. Stream layout results also
// expire at the next layout pass on that stream; copy results that must outlive
// these boundaries. Trim also invalidates results whose storage it discards. A Workspace must not be used concurrently.
type Workspace struct {
	streams      []*Stream
	streamCursor int
	streamUsed   int
}

// Reset invalidates borrowed results and starts a fresh lease cycle.
func (w *Workspace) Reset() {
	w.Release()

	w.streamCursor = 0
	w.streamUsed = 0
}

// Stream leases a stream using options; its storage is borrowed.
func (w *Workspace) Stream(options StreamOptions) *Stream {
	index := w.streamCursor
	w.streamCursor++
	w.streamUsed = max(w.streamUsed, w.streamCursor)
	if index == len(w.streams) {
		w.streams = append(w.streams, new(Stream))
	}
	s := w.streams[index]
	clear(s.instructions)
	s.instructions = s.instructions[:0]
	s.sizes = s.sizes[:0]
	s.marks = s.marks[:0]
	o := options
	*s = Stream{pooled: true, instructionHistory: s.instructionHistory, sizeHistory: s.sizeHistory, markHistory: s.markHistory, instructions: s.instructions, sizes: s.sizes, marks: s.marks, layout: s.layout, size: o.Size, flagEffect: o.FlagEffect, flagFacts: o.FlagFacts, exactFlagFacts: o.ExactFlagFacts, binarySetsFlags: o.BinarySetsFlags, decSetsCarry: o.DecSetsCarry}
	return s
}

// Rewind restarts stream leases after the first keepStreams streams. It
// invalidates the discarded stream leases, retaining storage for reuse. The preserved prefix must belong to the current Reset cycle.
func (w *Workspace) Rewind(keepStreams int) {
	if keepStreams < 0 || keepStreams > w.streamCursor {
		panic("machine: invalid workspace rewind")
	}
	if workspacePoison {
		for _, s := range w.streams[keepStreams:w.streamCursor] {
			s.releaseWork(true)
		}
	} else {
		for _, s := range w.streams[keepStreams:w.streamCursor] {
			s.instructionHistory.Observe(len(s.instructions))
			s.sizeHistory.Observe(len(s.sizes))
			s.markHistory.Observe(len(s.marks))
		}
	}
	w.streamCursor = keepStreams
}

// RetainedBytes reports backing-buffer capacities, and whether any exceeds limit. Fixed Workspace and lease metadata
// are excluded. Trim uses the same accounting.
func (w *Workspace) RetainedBytes(limit int) (int, bool) {
	total := 0
	big := false
	for _, stream := range w.streams {
		for _, n := range stream.workSizes() {
			total += n
			big = big || n > limit
		}
	}
	return total, big
}

// Trim discards unused leases and every buffer larger than limit, using the
// same byte accounting as RetainedBytes. Discarded storage invalidates its leases.
func (w *Workspace) Trim(limit int) {
	clear(w.streams[w.streamUsed:])
	w.streams = w.streams[:w.streamUsed]
	for _, s := range w.streams {
		s.trim(limit)
	}
}

// Release invalidates borrowed results and clears references. A build with
// nyanjit_scratch_poison overwrites retained storage to detect stale borrows.
func (w *Workspace) Release() {
	for _, stream := range w.streams {
		stream.releaseWork(workspacePoison)
	}
	clear(w.streams[w.streamUsed:])
	w.streams = w.streams[:w.streamUsed]
}
func workBytes[T any](v []T) int { var item T; return cap(v) * int(unsafe.Sizeof(item)) }
func workReset[T any](v *[]T, n int) []T {
	if cap(*v) < n {
		*v = make([]T, n)
	} else {
		if len(*v) > n {
			clear((*v)[n:])
		}
		*v = (*v)[:n]
		clear(*v)
	}
	return *v
}
func workAppend[T any](v *[]T, n int) []T {
	clear(*v)
	if cap(*v) < n {
		*v = make([]T, 0, n)
	} else {
		*v = (*v)[:0]
	}
	return *v
}

func workTrim[T any](v *[]T, limit int) {
	if workBytes(*v) > limit {
		if workspacePoison {
			poisonDiscarded(*v)
		}
		*v = nil
	}
}

func (s *Stream) trim(limit int) {
	workTrim(&s.instructions, limit)
	workTrim(&s.sizes, limit)
	workTrim(&s.marks, limit)
	workTrim(&s.layout.located, limit)
	workTrim(&s.layout.old, limit)
	workTrim(&s.layout.kept, limit)
	workTrim(&s.layout.relax, limit)
	workTrim(&s.layout.positions, limit)
	workTrim(&s.layout.labelIndex, limit)
	workTrim(&s.layout.oldToNew, limit)
	workTrim(&s.layout.newPositions, limit)
	workTrim(&s.layout.sizes, limit)
	workTrim(&s.layout.pending, limit)
	workTrim(&s.layout.remove, limit)
	workTrim(&s.layout.referenced, limit)
	workTrim(&s.layout.reachable, limit)
	workTrim(&s.layout.threadNext, limit)
	workTrim(&s.layout.threadDest, limit)
	workTrim(&s.layout.threadPath, limit)
	workTrim(&s.layout.threadState, limit)
	workTrim(&s.layout.threadCyclic, limit)
}

// Decay forgets excess per-buffer capacity after sustained smaller lease cycles.
// Call it once after finishing each cycle, before Release or Reset. A recurring
// large request within a few cycles keeps its storage. Discarded leases expire.
func (w *Workspace) Decay() {
	for _, s := range w.streams {
		s.decay()
	}
}

func workDecay[T any](v *[]T, h *BufferHistory) {
	excess := h.FinishCycle(cap(*v))
	if workBytes(*v) > 4096 && excess {
		*v = nil
	}
}
func workResetTracked[T any](v *[]T, h *BufferHistory, n int) []T {
	h.Observe(n)
	return workReset(v, n)
}
func workAppendTracked[T any](v *[]T, h *BufferHistory, n int) []T {
	h.Observe(n)
	return workAppend(v, n)
}
func (s *Stream) decay() {
	workDecay(&s.instructions, &s.instructionHistory)
	workDecay(&s.sizes, &s.sizeHistory)
	workDecay(&s.marks, &s.markHistory)
	workDecay(&s.layout.located, &s.layout.history.located)
	workDecay(&s.layout.old, &s.layout.history.old)
	workDecay(&s.layout.kept, &s.layout.history.kept)
	workDecay(&s.layout.relax, &s.layout.history.relax)
	workDecay(&s.layout.positions, &s.layout.history.positions)
	workDecay(&s.layout.labelIndex, &s.layout.history.labelIndex)
	workDecay(&s.layout.oldToNew, &s.layout.history.oldToNew)
	workDecay(&s.layout.newPositions, &s.layout.history.newPositions)
	workDecay(&s.layout.sizes, &s.layout.history.sizes)
	workDecay(&s.layout.pending, &s.layout.history.pending)
	workDecay(&s.layout.remove, &s.layout.history.remove)
	workDecay(&s.layout.referenced, &s.layout.history.referenced)
	workDecay(&s.layout.reachable, &s.layout.history.reachable)
	workDecay(&s.layout.threadNext, &s.layout.history.threadNext)
	workDecay(&s.layout.threadDest, &s.layout.history.threadDest)
	workDecay(&s.layout.threadPath, &s.layout.history.threadPath)
	workDecay(&s.layout.threadState, &s.layout.history.threadState)
	workDecay(&s.layout.threadCyclic, &s.layout.history.threadCyclic)
}

// TrimPointers discards only pointer-bearing instruction buffers
// above limit, invalidating affected borrows. Use Trim to bound all buffers
// measured by RetainedBytes.
func (w *Workspace) TrimPointers(limit int) {
	for _, s := range w.streams {
		workTrim(&s.instructions, limit)
		workTrim(&s.layout.old, limit)
		workTrim(&s.layout.kept, limit)
		workTrim(&s.layout.relax, limit)
		workTrim(&s.layout.located, limit)
	}
}

// Trim invalidates exposed slices too. Unlike reuse, discard must leave the
// old backing array poisoned for any caller still holding an expired borrow.
func poisonDiscarded[T any](buffer []T) {
	switch values := any(buffer[:cap(buffer)]).(type) {
	case []Instruction:
		for i := range values {
			values[i] = Instruction{Op: 255, Symbol: "expired workspace"}
		}
	case []LocatedInstruction:
		for i := range values {
			values[i] = LocatedInstruction{Instruction: Instruction{Op: 255}, PC: -0x5a5a}
		}
	case []int:
		for i := range values {
			values[i] = -0x5a5a
		}
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
