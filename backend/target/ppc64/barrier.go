package ppc64

import "github.com/moriyoshi/nyanjit/backend/machine"

// The speculation barrier is the one Linux patches in for barrier_nospec. It
// takes effect only once host firmware enables it.
var barriers = [...]struct {
	contract machine.BarrierContract
	op       Opcode
}{
	machine.BarrierSpeculation: {machine.BarrierPlatform, SpeculationBarrier},
	machine.BarrierMemory:      {machine.BarrierArchitectural, Sync},
	machine.BarrierInstruction: {machine.BarrierArchitectural, Isync},
}

// Barrier reports what the target's barrier for kind rests on.
func (Policy) Barrier(kind machine.Barrier) machine.BarrierContract {
	if !kind.Valid() || int(kind) >= len(barriers) {
		return machine.BarrierUnavailable
	}
	return barriers[kind].contract
}

func (p *expansion) barrier(kind machine.Barrier) error {
	if !kind.Valid() {
		return machine.ErrInvalidInstruction
	}
	if (Policy{}).Barrier(kind) == machine.BarrierUnavailable {
		return machine.ErrUnsupportedOperation
	}
	p.emit(Instruction{Op: barriers[kind].op})
	return nil
}
