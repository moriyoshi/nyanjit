package riscv64

import "github.com/moriyoshi/nyanjit/backend/machine"

// No ratified RISC-V instruction stops speculation; memory fences do not, and
// FENCE.I only happens to on some cores. FENCE.I is in RV64G (Zifencei).
var barriers = [...]struct {
	contract machine.BarrierContract
	word     uint32
}{
	machine.BarrierMemory:      {machine.BarrierArchitectural, 0x0330000f}, // fence rw, rw
	machine.BarrierInstruction: {machine.BarrierArchitectural, 0x0000100f}, // fence.i
}

// Barrier reports what the target's barrier for kind rests on.
func (Policy) Barrier(kind machine.Barrier) machine.BarrierContract {
	if !kind.Valid() || int(kind) >= len(barriers) {
		return machine.BarrierUnavailable
	}
	return barriers[kind].contract
}

// BarrierSize is the encoded size of kind's barrier, or 0 when unavailable.
func BarrierSize(kind machine.Barrier) int {
	if (Policy{}).Barrier(kind) == machine.BarrierUnavailable {
		return 0
	}
	return 4
}

func (e *ByteAssembler) Barrier(kind machine.Barrier) error {
	if !e.require(kind.Valid()) {
		return e.err
	}
	if BarrierSize(kind) == 0 {
		return machine.ErrUnsupportedOperation
	}
	e.words = append(e.words, barriers[kind].word)
	return nil
}
