package loong64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// NarrowMemory emits a 16/32-bit scalar access. Addresses on strict-alignment
// ISAs must be naturally aligned; the compiler legalizes arbitrary alignment.
// Loads zero-extend; stores truncate. A/B are dst/address or address/value.
func (e *ByteAssembler) NarrowMemory(i machine.Instruction) error {
	if !e.require((validInstructionRegisters(i))) {
		return e.err
	}

	if !i.ByteOrder.Valid() || i.ByteOrder == op.ByteOrderBigEndian {
		return machine.ErrUnsupportedOperation
	}
	load := i.Op == machine.OpLoad16 || i.Op == machine.OpLoad32
	bits := uint8(16)
	switch i.Op {
	case machine.OpLoad16, machine.OpStore16:
	case machine.OpLoad32, machine.OpStore32:
		bits = 32
	default:
		return machine.ErrUnsupportedOperation
	}
	d, a := i.A, i.B
	if !load {
		d, a = i.B, i.A
	}
	e.memory(load, bits, d, a, 0)
	return nil
}
