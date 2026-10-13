package compile

import (
	"slices"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

type scratchDiscard struct {
	bytes int
	drop  func()
}

func discardSlice[T any](out *[]scratchDiscard, value *[]T) {
	if n := bufferBytes(*value); n != 0 {
		*out = append(*out, scratchDiscard{n, func() { *value = nil }})
	}
}
func (s *compileScratch) trimTotalBudget(total, machineBytes int) {
	var choices []scratchDiscard
	discardSlice(&choices, &s.allocation.homeValues)
	discardSlice(&choices, &s.allocation.homeGenerations)
	discardSlice(&choices, &s.allocation.definedAt)
	discardSlice(&choices, &s.allocation.previousCall)
	discardSlice(&choices, &s.allocation.entryWords)
	discardSlice(&choices, &s.allocation.liveFP)
	discardSlice(&choices, &s.allocation.homesSaved)
	discardSlice(&choices, &s.allocation.registers)
	discardSlice(&choices, &s.control.fragments)
	discardSlice(&choices, &s.control.exportSlots)
	discardSlice(&choices, &s.control.exportHomes)
	discardSlice(&choices, &s.control.results)
	discardSlice(&choices, &s.control.positions)
	discardSlice(&choices, &s.control.incoming)
	discardSlice(&choices, &s.control.resultRegisters)
	discardSlice(&choices, &s.control.resultWords)
	discardSlice(&choices, &s.control.resultWordCounts)
	discardSlice(&choices, &s.control.flagReports)
	discardSlice(&choices, &s.control.bound)
	discardSlice(&choices, &s.control.labels)
	discardSlice(&choices, &s.control.steps)
	discardSlice(&choices, &s.control.labelSlots)
	discardSlice(&choices, &s.control.labelRegisters)
	discardSlice(&choices, &s.control.labelWords)
	discardSlice(&choices, &s.homes.plan.Slots)
	discardSlice(&choices, &s.homes.plan.Words)
	discardSlice(&choices, &s.homes.plan.VariableRegions)
	discardSlice(&choices, &s.homes.plan.VariableBits)
	discardSlice(&choices, &s.homes.plan.EntrySlots)
	discardSlice(&choices, &s.homes.demand)
	discardSlice(&choices, &s.homes.fpDemand)
	discardSlice(&choices, &s.homes.ending)
	discardSlice(&choices, &s.homes.scopes)
	discardSlice(&choices, &s.homes.banks)
	discardSlice(&choices, &s.homes.bankData)
	discardSlice(&choices, &s.homes.cursors)
	discardSlice(&choices, &s.homes.scopeStack)
	discardSlice(&choices, &s.homes.outer)
	discardSlice(&choices, &s.homes.localBanks)
	discardSlice(&choices, &s.homes.bankCounts)
	discardSlice(&choices, &s.homes.visibleCounts)
	discardSlice(&choices, &s.homes.active)
	discardSlice(&choices, &s.homes.candidates)
	discardSlice(&choices, &s.homes.free)
	discardSlice(&choices, &s.homes.queues[0].minimum)
	discardSlice(&choices, &s.homes.queues[1].minimum)
	b := &s.homes
	if b.plan.Captures != nil {
		choices = append(choices, scratchDiscard{retainedMapBytes[int, bool](b.peakVariables), func() { b.plan.Captures = nil }})
	}
	if b.plan.RegisterOverrides != nil {
		choices = append(choices, scratchDiscard{retainedMapBytes[int, map[int]machine.Home](b.peakScopes), func() { b.plan.RegisterOverrides = nil }})
	}
	if b.plan.Modified != nil {
		choices = append(choices, scratchDiscard{retainedMapBytes[int, map[int]bool](b.peakScopes), func() { b.plan.Modified = nil }})
	}
	if b.plan.Parents != nil {
		choices = append(choices, scratchDiscard{retainedMapBytes[int, int](b.peakScopes), func() { b.plan.Parents = nil }})
	}
	if b.plan.Reserved != nil {
		choices = append(choices, scratchDiscard{retainedMapBytes[backend.Register, bool](len(b.plan.Reserved)), func() { b.plan.Reserved = nil }})
	}
	if b.plan.ReservedRegions != nil {
		choices = append(choices, scratchDiscard{retainedMapBytes[int, [4]uint64](b.peakScopes), func() { b.plan.ReservedRegions = nil }})
	}
	if b.plan.RegisterVariables != nil {
		choices = append(choices, scratchDiscard{retainedMapBytes[int, []int](b.peakScopes), func() { b.plan.RegisterVariables = nil }})
	}
	// Rows share their metadata array; drop independent map/ID buffers first.
	clear(s.homes.plan.RegisterOverrides)
	clear(s.homes.plan.Modified)
	clear(s.homes.plan.RegisterVariables)
	for i := range s.homes.rows {
		row := &s.homes.rows[i]
		if n := row.retained(); n != 0 {
			choices = append(choices, scratchDiscard{n, func() { *row = homeRow{} }})
		}
	}
	s.selection.discards(&choices)
	slices.SortFunc(choices, func(a, b scratchDiscard) int { return b.bytes - a.bytes })
	_, machineLargest := s.machine.TrimToBudget(machineBytes)
	// The machine workspace and the register allocator discard their own
	// buffers, largest first; interleave them with the other candidates.
	allocator := &s.allocation.allocator
	allocatorBytes, _ := allocator.RetainedBytes(0)
	_, allocatorLargest := allocator.TrimToBudget(allocatorBytes)
	trimLarger := func(bytes int) {
		for total > scratchTotalLimit && max(machineLargest, allocatorLargest) > bytes {
			var freed int
			if machineLargest >= allocatorLargest {
				freed, machineLargest = s.machine.TrimToBudget(machineBytes - 1)
				machineBytes -= freed
			} else {
				freed, allocatorLargest = allocator.TrimToBudget(allocatorBytes - 1)
				allocatorBytes -= freed
			}
			if freed == 0 {
				return
			}
			total -= freed
		}
	}
	for _, candidate := range choices {
		trimLarger(candidate.bytes)
		if total <= scratchTotalLimit {
			return
		}
		candidate.drop()
		total -= candidate.bytes
	}
	trimLarger(0)
	if total > scratchTotalLimit {
		s.machine.TrimToBudget(max(0, machineBytes-(total-scratchTotalLimit)))
	}
}
