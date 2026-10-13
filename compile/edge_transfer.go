package compile

import "github.com/moriyoshi/nyanjit/regalloc"

// edgeTransferPlan keeps the emitter's physical-register view of an
// architecture-independent transfer plan.
type edgeTransferPlan struct {
	scratch       physReg
	scratchSave   int
	snapshots     map[int]int
	registerCycle int
	slots         int
}

func planEdgeTransfer(ids []int, destinationRegisters, destinationSlots []int,
	homes map[int]exportLocation, registers []physReg, preferredScratch physReg) (edgeTransferPlan, error) {
	available := make([]int, len(registers))
	for i, register := range registers {
		available[i] = int(register)
	}
	plan, err := regalloc.PlanEdgeTransfer(ids, destinationRegisters, destinationSlots,
		homes, available, int(preferredScratch))
	return edgeTransferPlan{
		scratch: physReg(plan.Scratch), scratchSave: plan.ScratchSave,
		snapshots: plan.Snapshots, registerCycle: plan.RegisterCycle,
		slots: plan.Slots,
	}, err
}
