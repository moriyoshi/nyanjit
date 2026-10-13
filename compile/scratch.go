package compile

import (
	"sync"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

// Scratch is private to one active lowering. Unit keeps only owned output.
// Bound retained storage both per buffer and per compile; a large compile must
// not pin its temporary memory, unless large compiles recur within
// scratchRecurrence lowerings (see streamLimits). sync.Pool may discard
// entries during GC.
const scratchBufferLimit = 4 << 20
const scratchTotalLimit = 32 << 20
const scratchPointerLimit = 1 << 20
const scratchRecurrence = 16

type compileScratch struct {
	allocation allocationTemps
	machine    *machine.Workspace
	selection  selectionWorkspace
	homes      homeBuffers
	control    controlBuffers
	retained   int
	// largeCredit counts down the lowerings since one held machine or
	// selection buffers over scratchPointerLimit.
	largeCredit uint8
	// arenaCredit does the same for control arenas over scratchBufferLimit.
	arenaCredit uint8
	pool        *sync.Pool // where putCompileScratch returns it
}

// compileScratchPool serves lowerings without a Target, such as internal
// tests; every Target has its own pool.
var compileScratchPool = newScratchPool()

func newScratchPool() *sync.Pool {
	return &sync.Pool{New: func() any { return new(compileScratch) }}
}

func getCompileScratch(options compileOptions) *compileScratch {
	pool := compileScratchPool
	if options.target != nil {
		pool = options.target.scratch
	}
	s := pool.Get().(*compileScratch)
	s.pool = pool
	if s.machine == nil {
		s.machine = new(machine.Workspace)
	}
	s.machine.Reset()
	s.selection.reset()
	s.homes.used = false
	return s
}
func putCompileScratch(s *compileScratch) {
	// Poison before trimming so a dangling output alias is still detected.
	if scratchPoison {
		s.machine.Release()
		s.selection.release()
	}
	// Control arenas over scratchBufferLimit stay while that demand recurs,
	// bounded by the total budget like recurring machine streams.
	arenaLimit := scratchBufferLimit
	if recurring(&s.arenaCredit, s.control.arenasLarge()) {
		arenaLimit = scratchTotalLimit
	}
	// Expensive decay scans are only needed after a large retained working set.
	// Histories still observe every request, so a recurring big compile refreshes
	// each buffer independently even through several tiny compiles.
	if s.retained > 256<<10 {
		s.allocation.decay()
		s.homes.decay()
		s.control.decay(arenaLimit)
		s.machine.Decay()
		s.selection.decay()
	} else {
		s.homes.observe()
	}
	// A total below the smallest hard bound cannot contain an oversized
	// individual buffer. Avoid repeated workspace scans for ordinary small IR.
	machineBytes, machineLarge := s.machine.RetainedBytes(scratchPointerLimit)
	selectionBytes, selectionLarge := s.selection.retainedBytes(scratchPointerLimit)
	homeBytes, homeBig := s.homes.retained()
	controlBytes, controlBig := s.control.retained(arenaLimit)
	total := s.allocation.retainedBytes() + machineBytes + selectionBytes + homeBytes + controlBytes
	limit, pointerLimit := s.streamLimits(machineLarge || selectionLarge)
	trimmed := total > scratchPointerLimit
	if trimmed {
		s.machine.Trim(limit)
		s.machine.TrimPointers(pointerLimit)
		s.selection.trim(limit)
		s.selection.trimPointers(pointerLimit)
		if s.allocation.oversized() || homeBig || controlBig {
			s.allocation.decay()
			s.homes.decay()
			s.control.decay(arenaLimit)
		}
		machineBytes, _ = s.machine.RetainedBytes(scratchBufferLimit)
		selectionBytes, _ = s.selection.retainedBytes(scratchBufferLimit)
		homeBytes, _ = s.homes.retained()
		controlBytes, _ = s.control.retained(arenaLimit)
		total = s.allocation.retainedBytes() + machineBytes + selectionBytes + homeBytes + controlBytes
		if total > scratchTotalLimit {
			s.trimTotalBudget(total, machineBytes)
		}
	}

	if scratchPoison {
		poisonAllocationTemps(&s.allocation)
	}
	if !scratchPoison {
		s.machine.Release()
		s.selection.release()
	}
	s.homes.release(scratchPoison)
	s.control.release(scratchPoison)
	if trimmed || scratchPoison {
		machineBytes, _ = s.machine.RetainedBytes(scratchBufferLimit)
		selectionBytes, _ = s.selection.retainedBytes(scratchBufferLimit)
		homeBytes, _ = s.homes.retained()
		controlBytes, _ = s.control.retained(arenaLimit)
		total = s.allocation.retainedBytes() + machineBytes + selectionBytes + homeBytes + controlBytes
	}
	s.retained = total

	if s.pool == nil {
		s.pool = compileScratchPool
	}
	s.pool.Put(s)
}

// streamLimits returns the per-buffer limits for machine and selection
// storage. Buffers over scratchPointerLimit normally go with the lowering that
// grew them. When an earlier lowering within scratchRecurrence cycles needed
// them too, the demand recurs: they stay, bounded only by the total budget, so
// a repeated large lowering reuses its streams instead of regrowing them.
// Decay still drops each one after sustained smaller lowerings.
func (s *compileScratch) streamLimits(large bool) (limit, pointerLimit int) {
	if recurring(&s.largeCredit, large) {
		return scratchTotalLimit, scratchTotalLimit
	}
	return scratchBufferLimit, scratchPointerLimit
}

// recurring records whether this lowering's demand is large and reports
// whether large demand already occurred within the last scratchRecurrence
// lowerings counted by credit.
func recurring(credit *uint8, large bool) bool {
	again := large && *credit > 0
	if large {
		*credit = scratchRecurrence
	} else if *credit > 0 {
		*credit--
	}
	return again
}

func bufferBytes[T any](s []T) int { var value T; return cap(s) * int(unsafe.Sizeof(value)) }
func resetBuffer[T any](s *[]T, n int) []T {
	if cap(*s) < n {
		*s = make([]T, n)
	} else {
		if len(*s) > n {
			clear((*s)[n:])
		}
		*s = (*s)[:n]
		clear(*s)
	}
	return *s
}

// carve returns an empty slice with room for n elements from arena. Appending
// past n reallocates, so carved slices never overlap. Chunks double, so small
// programs allocate little and large ones few times.
func carve[T any](arena *[]T, n int) []T {
	if cap(*arena)-len(*arena) < n {
		*arena = make([]T, 0, max(n, 2*cap(*arena), 8))
	}
	start := len(*arena)
	*arena = (*arena)[:start+n]
	return (*arena)[start : start : start+n]
}

// keepArena returns chunk, emptied, to keep as a scratch arena for a later
// pass that demands as much as this one did.
func keepArena[T any](chunk []T, h *machine.BufferHistory, demand int) []T {
	h.Observe(demand)
	if cap(chunk) < demand {
		return make([]T, 0, demand)
	}
	return chunk[:0]
}

// carveCopy copies src into arena, keeping nil for an empty source.
func carveCopy[T any](arena *[]T, src []T) []T {
	if len(src) == 0 {
		return nil
	}
	return append(carve(arena, len(src)), src...)
}

func appendBuffer[T any](s *[]T, n int) []T {
	clear(*s)
	if cap(*s) < n {
		*s = make([]T, 0, n)
	} else {
		*s = (*s)[:0]
	}
	return *s
}

// Compiler and machine scratch share the same demand history and band policy.
func decayBuffer[T any](v *[]T, h *machine.BufferHistory) {
	decayBufferWithin(v, h, scratchBufferLimit)
}

// decayBufferWithin is decayBuffer with limit in place of scratchBufferLimit.
func decayBufferWithin[T any](v *[]T, h *machine.BufferHistory, limit int) {
	excess := h.FinishCycle(cap(*v))
	if bufferBytes(*v) > limit || bufferBytes(*v) > 4096 && excess {
		*v = nil
	}
}
func resetBufferTracked[T any](v *[]T, h *machine.BufferHistory, n int) []T {
	h.Observe(n)
	return resetBuffer(v, n)
}
func appendBufferTracked[T any](v *[]T, h *machine.BufferHistory, n int) []T {
	h.Observe(n)
	return appendBuffer(v, n)
}

// Swiss maps grow in groups of eight slots, with at most seven occupied.
// Include slot alignment, group control bytes, table/directory overhead and a
// 25% allowance for uneven table splits. Counts are high-water counts, so clear
// does not make retained buckets disappear from this conservative estimate.
func retainedMapBytes[K comparable, V any](count int) int {
	var k K
	var v V
	align := max(int(unsafe.Alignof(k)), int(unsafe.Alignof(v)))
	slot := (int(unsafe.Sizeof(k)) + int(unsafe.Sizeof(v)) + align - 1) / align * align
	groups := 0
	if count > 0 {
		groups = 1
		for groups*7 < count {
			groups *= 2
		}
	}
	return 64 + (groups*(8+8*slot)+64*((groups+127)/128))*5/4
}
