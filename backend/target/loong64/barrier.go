package loong64

import "github.com/moriyoshi/nyanjit/backend/machine"

// LoongArch defines no speculation barrier. Loongson states that its cores
// mitigate bounds-check bypass internally, but that is not an ISA contract.
var barriers = [...]struct {
	contract machine.BarrierContract
	word     uint32
	text     string
}{
	machine.BarrierMemory:      {machine.BarrierArchitectural, 0x38720000, "dbar 0"},
	machine.BarrierInstruction: {machine.BarrierArchitectural, 0x38728000, "ibar 0"},
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
	e.emit(barriers[kind].word, "%s", barriers[kind].text)
	return nil
}
