//go:build linux && (arm64 || amd64)

package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// controlOptions are host lowering options for the control-flow backend tests.
var controlOptions = uncheckedTarget(TargetSpec{}).options(Func{ABI: abi.C, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})

func requireControlOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestPlanEdgeTransferSnapshotsStackSwap(t *testing.T) {
	registers := newMachineTarget(controlOptions).Registers().AppendTo(nil)
	homes := map[int]exportLocation{
		0: {Register: -1, Slot: 0},
		1: {Register: -1, Slot: 1},
	}
	plan, err := planEdgeTransfer([]int{0, 1}, []int{-1, -1}, []int{1, 0},
		homes, registers, newMachineTarget(controlOptions).ScratchRegister())
	requireControlOK(t, err)
	if len(plan.snapshots) != 2 || plan.slots != 2 || len(plan.snapshotSources) != 2 || plan.snapshotSources[0] != 0 || plan.snapshotSources[1] != 1 {
		t.Fatalf("stack swap plan = %+v, want two source snapshots", plan)
	}
	if !edgeCanUseHomes([]int{0, 1}, []int{-1, -1}, []int{1, 0},
		[]int{0, 1}, []exportLocation{homes[0], homes[1]}, registers,
		newMachineTarget(controlOptions).ScratchRegister()) {
		t.Fatal("stack swap unexpectedly selected the staged edge path")
	}
}

func TestPlanEdgeTransferPreservesScratchSource(t *testing.T) {
	target := newMachineTarget(controlOptions)
	registers := target.Registers().AppendTo(nil)
	ids := make([]int, len(registers)+1)
	destinations := make([]int, len(ids))
	slots := make([]int, len(ids))
	homes := make(map[int]exportLocation, len(ids))
	for i, register := range registers {
		ids[i], destinations[i], slots[i] = i, int(register), -1
		homes[i] = exportLocation{Register: int(register), Slot: -1}
	}
	last := len(registers)
	ids[last], destinations[last], slots[last] = last, -1, 1
	homes[last] = exportLocation{Register: -1, Slot: 0}
	plan, err := planEdgeTransfer(ids, destinations, slots, homes, registers, target.ScratchRegister())
	requireControlOK(t, err)
	if plan.scratchSave < 0 || plan.slots != 1 {
		t.Fatalf("scratch source plan = %+v, want one saved register", plan)
	}
}

func TestPlanEdgeTransferSnapshotsRegisterDestinationSource(t *testing.T) {
	target := newMachineTarget(controlOptions)
	registers := target.Registers().AppendTo(nil)
	homes := map[int]exportLocation{
		0: {Register: -1, Slot: 0},
		1: {Register: int(registers[1]), Slot: -1},
	}
	plan, err := planEdgeTransfer([]int{0, 1}, []int{int(registers[0]), -1}, []int{-1, 0},
		homes, registers, target.ScratchRegister())
	requireControlOK(t, err)
	if _, ok := plan.snapshots[0]; !ok {
		t.Fatalf("source-overwrite plan = %+v, want slot 0 snapshot", plan)
	}
}

func TestCompactControlStreamThreadsEmptyJumpChain(t *testing.T) {
	stream := newMachineEmitter(controlOptions).(*physicalEmitter)
	labels := make([]int, 4)
	for i := 0; i < 3; i++ {
		labels[i] = stream.machineStream().PC()
		stream.append(physicalInsn{Op: pJump, Imm: uint64(i + 1)})
	}
	labels[3] = stream.machineStream().PC()
	stream.append(physicalInsn{Op: pReturn})
	updated, err := compactControlStream(stream, labels, []bool{true, true, true, true})
	requireControlOK(t, err)
	if len(stream.machineStream().Instructions()) != 1 || stream.machineStream().Instructions()[0].Op != pReturn {
		t.Fatalf("compacted stream = %+v, want only return", stream.machineStream().Instructions())
	}
	for i, position := range updated {
		if position != 0 {
			t.Fatalf("label %d PC = %d, want 0", i, position)
		}
	}
}
