package i386

import "github.com/moriyoshi/nyanjit/backend/machine"

// LFENCE does not dispatch later instructions until earlier ones complete
// when the processor is configured for it (some AMD parts need an MSR set by
// the operating system). Code modified by another thread needs a serializing
// instruction, and none of those leaves registers untouched in the supported
// baseline, so x86 offers no BarrierInstruction.
var barriers = [...]struct {
	contract machine.BarrierContract
	code     []byte
	text     string
}{
	machine.BarrierSpeculation: {machine.BarrierPlatform, []byte{0x0f, 0xae, 0xe8}, "lfence"},
	machine.BarrierMemory:      {machine.BarrierArchitectural, []byte{0x0f, 0xae, 0xf0}, "mfence"},
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
	return len(barriers[kind].code)
}

func (e *ByteAssembler) Barrier(kind machine.Barrier) error {
	if !e.require(kind.Valid()) {
		return e.err
	}
	if BarrierSize(kind) == 0 {
		return machine.ErrUnsupportedOperation
	}
	e.code = append(e.code, barriers[kind].code...)
	return nil
}

func (e *TextAssembler) Barrier(kind machine.Barrier) error {
	if err := e.ByteAssembler.Barrier(kind); err != nil {
		return err
	}
	e.record("%s", barriers[kind].text)
	return nil
}
