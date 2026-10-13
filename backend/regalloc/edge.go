package regalloc

import "github.com/moriyoshi/nyanjit/backend"

// Home describes where an outgoing virtual value resides before an edge.
type Home struct {
	Register  int
	Slot      int
	Constant  bool
	Immediate uint64
	Float     bool  // Slot holds the native FP register representation, not integer bits.
	Bits      uint8 // Constant scalar width; zero denotes 64 bits.
}

// EdgePlan records temporary space needed for a parallel edge transfer.
// Snapshot slots are relative to the caller's edge temporary area; Snapshots
// is nil when no source slot needs preserving.
type EdgePlan struct {
	Scratch       int
	ScratchSave   int
	Snapshots     map[int]int
	RegisterCycle int
	Slots         int
}

// PlanEdgeTransfer chooses scratch storage before frame sizing. It preserves
// source slots that destinations overwrite and reserves a slot for cycles
// when every candidate register is occupied.
func PlanEdgeTransfer(ids []int, destinationRegisters, destinationSlots []int,
	homes map[int]Home, registers []int, preferredScratch int) (EdgePlan, error) {
	plan := EdgePlan{Scratch: preferredScratch, ScratchSave: -1, RegisterCycle: -1}
	if len(ids) != len(destinationRegisters) || len(ids) != len(destinationSlots) {
		return plan, backend.ErrInvalidFunction
	}
	sourceRegisters := make(map[int]bool)
	changedSlots := make(map[int]bool)
	needsScratch := false
	for i, id := range ids {
		home, ok := homes[id]
		if !ok || home.Register < 0 && home.Slot < 0 && !home.Constant {
			return plan, backend.ErrInvalidFunction
		}
		if home.Register >= 0 {
			sourceRegisters[home.Register] = true
		}
		if destinationRegisters[i] >= 0 {
			continue
		}
		if destinationSlots[i] < 0 {
			return plan, backend.ErrInvalidFunction
		}
		if home.Register >= 0 {
			changedSlots[destinationSlots[i]] = true
		} else if home.Slot >= 0 {
			if home.Slot != destinationSlots[i] {
				changedSlots[destinationSlots[i]] = true
				needsScratch = true
			}
		} else {
			changedSlots[destinationSlots[i]] = true
			needsScratch = true
		}
	}
	for _, id := range ids {
		home := homes[id]
		if home.Register >= 0 {
			continue
		}
		if home.Slot >= 0 && changedSlots[home.Slot] {
			if _, exists := plan.Snapshots[home.Slot]; !exists {
				if plan.Snapshots == nil {
					plan.Snapshots = make(map[int]int)
				}
				plan.Snapshots[home.Slot] = plan.Slots
				plan.Slots++
			}
		}
	}
	if len(plan.Snapshots) != 0 {
		needsScratch = true
	}
	if needsScratch && sourceRegisters[plan.Scratch] {
		found := false
		for _, candidate := range registers {
			if !sourceRegisters[candidate] {
				plan.Scratch, found = candidate, true
				break
			}
		}
		if !found {
			plan.ScratchSave = plan.Slots
			plan.Slots++
		}
	}
	type registerMove struct{ src, dst int }
	pending := make([]registerMove, 0, len(ids))
	occupied := make(map[int]bool)
	for i, id := range ids {
		if destinationRegisters[i] < 0 {
			continue
		}
		dst := destinationRegisters[i]
		occupied[dst] = true
		home := homes[id]
		if home.Register < 0 || plan.ScratchSave >= 0 && home.Register == plan.Scratch {
			continue
		}
		src := home.Register
		occupied[src] = true
		if src != dst {
			pending = append(pending, registerMove{src, dst})
		}
	}
	for len(pending) != 0 {
		ready := -1
		for i, move := range pending {
			blocked := false
			for j, other := range pending {
				blocked = blocked || i != j && other.src == move.dst
			}
			if !blocked {
				ready = i
				break
			}
		}
		if ready < 0 {
			free := false
			for _, candidate := range registers {
				free = free || !occupied[candidate]
			}
			if !free {
				plan.RegisterCycle = plan.Slots
				plan.Slots++
			}
			break
		}
		pending = append(pending[:ready], pending[ready+1:]...)
	}
	return plan, nil
}
