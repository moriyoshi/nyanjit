package arm64

import "github.com/moriyoshi/nyanjit/backend/machine"

// DSB SY; ISB is Arm's speculation barrier for cores without FEAT_SB: no
// later instruction executes speculatively past it.
var barriers = [...]struct {
	contract machine.BarrierContract
	words    []uint32
	text     []string
}{
	machine.BarrierSpeculation: {machine.BarrierArchitectural, []uint32{0xd5033f9f, 0xd5033fdf}, []string{"dsb sy", "isb"}},
	machine.BarrierMemory:      {machine.BarrierArchitectural, []uint32{0xd5033bbf}, []string{"dmb ish"}},
	machine.BarrierInstruction: {machine.BarrierArchitectural, []uint32{0xd5033fdf}, []string{"isb"}},
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
	return 4 * len(barriers[kind].words)
}

func (e *ByteAssembler) Barrier(kind machine.Barrier) error {
	if !e.require(kind.Valid()) {
		return e.err
	}
	if BarrierSize(kind) == 0 {
		return machine.ErrUnsupportedOperation
	}
	e.words = append(e.words, barriers[kind].words...)
	return nil
}

func (e *TextAssembler) Barrier(kind machine.Barrier) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.Barrier(kind) }); err != nil {
		return err
	}
	for _, text := range barriers[kind].text {
		e.line("%s", text)
	}
	return nil
}
