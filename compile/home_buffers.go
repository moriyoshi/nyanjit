package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

type homeRow struct {
	homes    map[int]machine.Home
	modified map[int]bool
	ids      []int
	// ids capacity bounds allocated map size.
	history machine.BufferHistory
}
type homeBuffers struct {
	history                                                                       struct{ slots, words, variableRegions, variableBits, entrySlots, rows, demand, fpDemand, ending, scopes, banks, bankData, cursors, scopeStack, outer, localBanks, bankCounts, visibleCounts, active, candidates, free, gpQueue, fpQueue machine.BufferHistory }
	used                                                                          bool
	mapHistory                                                                    struct{ variables, scopes machine.BufferHistory }
	plan                                                                          machine.HomePlan
	rows                                                                          []homeRow
	demand, fpDemand, ending, scopes, banks, bankData, cursors, scopeStack, outer []int
	localBanks                                                                    [][2][]int
	bankCounts, visibleCounts                                                     [][2]int
	active                                                                        []bool
	candidates, free                                                              []physReg
	queues                                                                        [2]homeCandidates
	peakVariables, peakScopes                                                     int
}

func reuseMap[K comparable, V any](m *map[K]V) {
	if *m == nil {
		*m = make(map[K]V)
	} else {
		clear(*m)
	}
}
func (b *homeBuffers) begin(variables, scopes int) *machine.HomePlan {
	b.used = true
	p := &b.plan
	b.peakVariables = max(b.peakVariables, variables)
	b.peakScopes = max(b.peakScopes, scopes)
	resetBuffer(&p.Slots, variables)
	resetBuffer(&p.Words, variables)
	resetBuffer(&p.VariableRegions, variables)
	resetBuffer(&p.VariableBits, variables)
	p.EntrySlots = appendBuffer(&p.EntrySlots, 16)
	reuseMap(&p.RegisterOverrides)
	reuseMap(&p.Parents)
	reuseMap(&p.Captures)
	reuseMap(&p.Reserved)
	reuseMap(&p.Modified)
	reuseMap(&p.ReservedRegions)
	reuseMap(&p.RegisterVariables)
	p.Parents[0] = -1
	p.ReservedSlots = 0
	p.HasFloatingHomes = false
	p.AdditionalRegisters = nil
	if cap(b.rows) < scopes {
		b.rows = append(b.rows[:cap(b.rows)], make([]homeRow, scopes-cap(b.rows))...)
	}
	if len(b.rows) > scopes {
		clear(b.rows[scopes:])
	}
	b.rows = b.rows[:scopes]
	for i := 0; i < scopes; i++ {
		reuseMap(&b.rows[i].homes)
		reuseMap(&b.rows[i].modified)
	}
	return p
}
func (b *homeBuffers) sizes() [25]int {
	p := &b.plan
	variableMaps, scopeMaps := 0, 0
	if p.Captures != nil {
		variableMaps += retainedMapBytes[int, bool](b.peakVariables)
	}
	if p.RegisterOverrides != nil {
		scopeMaps += retainedMapBytes[int, map[int]machine.Home](b.peakScopes)
	}
	if p.Modified != nil {
		scopeMaps += retainedMapBytes[int, map[int]bool](b.peakScopes)
	}
	if p.Parents != nil {
		scopeMaps += retainedMapBytes[int, int](b.peakScopes)
	}
	if p.Reserved != nil {
		scopeMaps += retainedMapBytes[backend.Register, bool](len(b.plan.Reserved))
	}
	if p.ReservedRegions != nil {
		scopeMaps += retainedMapBytes[int, [4]uint64](b.peakScopes)
	}
	if p.RegisterVariables != nil {
		scopeMaps += retainedMapBytes[int, []int](b.peakScopes)
	}

	return [25]int{bufferBytes(p.Slots), bufferBytes(p.Words), bufferBytes(p.VariableRegions), bufferBytes(p.VariableBits), bufferBytes(p.EntrySlots), bufferBytes(b.rows), bufferBytes(b.demand), bufferBytes(b.fpDemand), bufferBytes(b.ending), bufferBytes(b.scopes), bufferBytes(b.banks), bufferBytes(b.bankData), bufferBytes(b.cursors), bufferBytes(b.scopeStack), bufferBytes(b.outer), bufferBytes(b.localBanks), bufferBytes(b.bankCounts), bufferBytes(b.visibleCounts), bufferBytes(b.active), bufferBytes(b.candidates), bufferBytes(b.free), bufferBytes(b.queues[0].minimum), bufferBytes(b.queues[1].minimum), variableMaps, scopeMaps}
}
func (b *homeBuffers) retained() (int, bool) {
	total := 0
	big := false
	for _, n := range b.sizes() {
		total += n
		big = big || n > scratchBufferLimit
	}
	for _, r := range b.rows {
		n := r.retained()
		total += n
		big = big || n > scratchBufferLimit
	}
	return total, big
}
func (b *homeBuffers) release(poison bool) {
	// These two fields borrow the immutable source facts, never pool ownership.
	b.plan.RegionBounds = nil
	b.plan.FloatHomes = nil
	clear(b.localBanks)
	b.localBanks = b.localBanks[:0]
	if !poison {
		return
	}
	for _, v := range [][]int{b.plan.Slots, b.plan.Words, b.plan.VariableRegions, b.plan.EntrySlots, b.demand, b.fpDemand, b.ending, b.scopes, b.banks, b.bankData, b.cursors, b.scopeStack, b.outer, b.queues[0].minimum, b.queues[1].minimum} {
		v = v[:cap(v)]
		for i := range v {
			v[i] = -0x5a5a
		}
	}
	for i := range b.plan.VariableBits[:cap(b.plan.VariableBits)] {
		b.plan.VariableBits[:cap(b.plan.VariableBits)][i] = 255
	}
	clear(b.plan.Captures)
	clear(b.plan.Reserved)
	clear(b.plan.Parents)
	for _, m := range b.plan.RegisterOverrides {
		clear(m)
	}
	clear(b.plan.RegisterOverrides)
	for _, m := range b.plan.Modified {
		clear(m)
	}
	clear(b.plan.Modified)
	clear(b.plan.ReservedRegions)
	clear(b.plan.RegisterVariables)
	clear(b.plan.Regions)
	for _, r := range b.rows[:cap(b.rows)] {
		clear(r.homes)
		clear(r.modified)
		for i := range r.ids[:cap(r.ids)] {
			r.ids[:cap(r.ids)][i] = -0x5a5a
		}
	}
	for _, buf := range [][]physReg{b.candidates, b.free} {
		for i := range buf[:cap(buf)] {
			buf[:cap(buf)][i] = 255
		}
	}
	for i := range b.active[:cap(b.active)] {
		b.active[:cap(b.active)][i] = true
	}
	for _, buf := range [][][2]int{b.bankCounts, b.visibleCounts} {
		for i := range buf[:cap(buf)] {
			buf[:cap(buf)][i] = [2]int{-0x5a5a, -0x5a5a}
		}
	}
	// Drop poisoned maps while retaining the overwritten scalar buffers. The
	// next lowering must reinitialize every buffer it reuses.
	b.plan.Captures = nil
	b.plan.Reserved = nil
	b.plan.Parents = nil
	b.plan.RegisterOverrides = nil
	b.plan.Modified = nil
	b.plan.ReservedRegions = nil
	b.plan.RegisterVariables = nil
	b.plan.Regions = nil
	for i := range b.rows {
		b.rows[i].homes = nil
		b.rows[i].modified = nil
		b.rows[i].history = machine.BufferHistory{}
	}
	b.peakVariables = 0
	b.peakScopes = 0

}

func (b *homeBuffers) decay() {
	b.observeBuffers()
	decayBuffer(&b.plan.Slots, &b.history.slots)
	decayBuffer(&b.plan.Words, &b.history.words)
	decayBuffer(&b.plan.VariableRegions, &b.history.variableRegions)
	decayBuffer(&b.plan.VariableBits, &b.history.variableBits)
	decayBuffer(&b.plan.EntrySlots, &b.history.entrySlots)
	decayBuffer(&b.rows, &b.history.rows)
	decayBuffer(&b.demand, &b.history.demand)
	decayBuffer(&b.fpDemand, &b.history.fpDemand)
	decayBuffer(&b.ending, &b.history.ending)
	decayBuffer(&b.scopes, &b.history.scopes)
	decayBuffer(&b.banks, &b.history.banks)
	decayBuffer(&b.bankData, &b.history.bankData)
	decayBuffer(&b.cursors, &b.history.cursors)
	decayBuffer(&b.scopeStack, &b.history.scopeStack)
	decayBuffer(&b.outer, &b.history.outer)
	decayBuffer(&b.localBanks, &b.history.localBanks)
	decayBuffer(&b.bankCounts, &b.history.bankCounts)
	decayBuffer(&b.visibleCounts, &b.history.visibleCounts)
	decayBuffer(&b.active, &b.history.active)
	decayBuffer(&b.candidates, &b.history.candidates)
	decayBuffer(&b.free, &b.history.free)
	decayBuffer(&b.queues[0].minimum, &b.history.gpQueue)
	decayBuffer(&b.queues[1].minimum, &b.history.fpQueue)

	if b.mapHistory.variables.FinishCycle(b.peakVariables) || retainedMapBytes[int, bool](b.peakVariables) > scratchBufferLimit {
		b.plan.Captures = nil
		b.peakVariables = 0
	}
	if b.mapHistory.scopes.FinishCycle(b.peakScopes) || retainedMapBytes[int, int](b.peakScopes) > scratchBufferLimit {
		b.plan.RegisterOverrides = nil
		b.plan.Parents = nil
		b.plan.Modified = nil
		b.plan.RegisterVariables = nil
		b.plan.ReservedRegions = nil
		b.peakScopes = 0
	}
	// Nested maps are owned by rows; discard old rows beyond the recent need.
	// Their references in the plan were invalidated before decay.
	for i := range b.rows[:cap(b.rows)] {
		r := &b.rows[:cap(b.rows)][i]
		need := 0
		if b.used && i < len(b.rows) {
			need = len(r.homes)
		}
		r.history.Observe(need)
		if r.history.FinishCycle(cap(r.ids)) || retainedMapBytes[int, machine.Home](cap(r.ids)) > scratchBufferLimit {
			r.homes = nil
			r.modified = nil
			r.ids = nil
			r.history = machine.BufferHistory{}
		}
	}
	b.used = false
}

func (b *homeBuffers) observeBuffers() {
	if !b.used {
		return
	}
	b.history.slots.Observe(len(b.plan.Slots))
	b.history.words.Observe(len(b.plan.Words))
	b.history.variableRegions.Observe(len(b.plan.VariableRegions))
	b.history.variableBits.Observe(len(b.plan.VariableBits))
	b.history.entrySlots.Observe(len(b.plan.EntrySlots))
	b.history.rows.Observe(len(b.rows))
	b.history.demand.Observe(len(b.demand))
	b.history.fpDemand.Observe(len(b.fpDemand))
	b.history.ending.Observe(len(b.ending))
	b.history.scopes.Observe(len(b.scopes))
	b.history.banks.Observe(len(b.banks))
	b.history.bankData.Observe(len(b.bankData))
	b.history.cursors.Observe(len(b.cursors))
	b.history.scopeStack.Observe(len(b.scopeStack))
	b.history.outer.Observe(len(b.outer))
	b.history.localBanks.Observe(len(b.localBanks))
	b.history.bankCounts.Observe(len(b.bankCounts))
	b.history.visibleCounts.Observe(len(b.visibleCounts))
	b.history.active.Observe(len(b.active))
	b.history.candidates.Observe(len(b.candidates))
	b.history.free.Observe(len(b.free))
	b.history.gpQueue.Observe(len(b.queues[0].minimum))
	b.history.fpQueue.Observe(len(b.queues[1].minimum))
	b.mapHistory.variables.Observe(len(b.plan.Words))
	b.mapHistory.scopes.Observe(len(b.plan.Parents))
}
func (b *homeBuffers) observe() {
	b.observeBuffers()
	if !b.used {
		return
	}

}

// The ID list contains every home; its retained capacity also bounds both
// maps' allocation high-water marks, even after clear reduces their lengths.
func (r *homeRow) retained() int {
	bytes := bufferBytes(r.ids)
	if r.homes != nil {
		bytes += retainedMapBytes[int, machine.Home](cap(r.ids))
	}
	if r.modified != nil {
		bytes += retainedMapBytes[int, bool](cap(r.ids))
	}
	return bytes
}
