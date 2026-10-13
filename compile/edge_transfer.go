package compile

import "github.com/moriyoshi/nyanjit/backend/regalloc"

// edgeTransferPlan keeps the emitter's physical-register view of an
// architecture-independent transfer plan.
type edgeTransferPlan struct {
	scratch         physReg
	scratchSave     int
	snapshots       map[int]int
	snapshotSources []int
	registerCycle   int
	slots           int
}

func planEdgeTransfer(ids []int, destinationRegisters, destinationSlots []int,
	homes map[int]exportLocation, registers []physReg, preferredScratch physReg) (edgeTransferPlan, error) {
	var availableBuffer [64]int
	available := availableBuffer[:0]
	for _, register := range registers {
		available = append(available, int(register))
	}
	plan, err := regalloc.PlanEdgeTransfer(ids, destinationRegisters, destinationSlots,
		homes, available, int(preferredScratch))
	snapshotSources := make([]int, len(plan.Snapshots))
	for source, relative := range plan.Snapshots {
		snapshotSources[relative] = source
	}
	return edgeTransferPlan{
		scratch: physReg(plan.Scratch), scratchSave: plan.ScratchSave,
		snapshots: plan.Snapshots, snapshotSources: snapshotSources, registerCycle: plan.RegisterCycle,
		slots: plan.Slots,
	}, err
}
