package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
)

// allocationMover emits the moves regalloc.Residency chooses for one
// selected program, and records the registers they write.
type allocationMover struct {
	values       []machine.SelectedValue
	emitter      machineEmitter
	registerBits uint8
	written      registerWrites
	// The spill-slot limit error for regalloc.Config, created once per
	// mover so a lowering does not allocate a closure.
	reservedSlots, slotLimit int
	limit                    func(int) error
}

func (m *allocationMover) release() { m.values, m.emitter = nil, nil }

func (m *allocationMover) limitError(reservedSlots, slotLimit int) func(int) error {
	m.reservedSlots, m.slotLimit = reservedSlots, slotLimit
	if m.limit == nil {
		m.limit = func(required int) error {
			return spillStorageLimitError(required, m.reservedSlots, m.slotLimit)
		}
	}
	return m.limit
}

func (m *allocationMover) Move(id int, to, from regalloc.Group) {
	for word := 0; word < int(to.Words); word++ {
		target := to.Registers[word]
		emitHomeMove(m.emitter, m.values[id].Bits, target, from.Registers[word])
		m.written[target] = true
	}
}

func (m *allocationMover) Spill(id int, slot int, from regalloc.Group) {
	value := m.values[id]
	switch value.Class {
	case backend.Float:
		m.emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatSpill, A: from.Registers[0], Slot: slot, Bits: value.Bits})
	case backend.Vector:
		m.emitter.vectorSpill(from.Registers[0], slot)
	default:
		for word := 0; word < int(from.Words); word++ {
			m.emitter.spill(from.Registers[word], slot+word)
		}
	}
}

func (m *allocationMover) Reload(id int, to regalloc.Group, slot int) {
	value := m.values[id]
	switch value.Class {
	case backend.Float:
		m.emitter.(*physicalEmitter).append(physicalInsn{Op: machine.OpFloatReload, A: to.Registers[0], Slot: slot, Bits: value.Bits})
	case backend.Vector:
		m.emitter.vectorReload(to.Registers[0], slot)
	default:
		for word := 0; word < int(to.Words); word++ {
			reg := to.Registers[word]
			m.emitter.reload(reg, slot+word)
			m.written[reg] = true
		}
	}
}

func (m *allocationMover) LoadIncoming(id int, to regalloc.Group, offsets [2]int) {
	for word := 0; word < int(to.Words); word++ {
		reg := to.Registers[word]
		m.emitter.incomingWord(reg, offsets[word])
		m.written[reg] = true
	}
}

func (m *allocationMover) Rematerialize(id int, to regalloc.Group) error {
	value := m.values[id]
	switch {
	case value.Symbol != "":
		if to.Words != 1 {
			return ErrBackendLimit
		}
		reg := to.Registers[0]
		m.emitter.symbol(reg, value.Symbol)
		m.written[reg] = true
	case value.Class == backend.Float:
		w := value.Bits
		if w == 0 {
			w = 64
		}
		m.emitter.(*physicalEmitter).literalValue(to.Registers[0], w, value.Imm)
	default:
		parts, err := regalloc.SplitIntegerWords(value.Imm, m.registerBits, int(to.Words))
		if err != nil {
			return err
		}
		for word := 0; word < int(to.Words); word++ {
			reg := to.Registers[word]
			m.emitter.constant(reg, parts[word])
			m.written[reg] = true
		}
	}
	return nil
}
