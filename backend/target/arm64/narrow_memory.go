package arm64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// NarrowMemory emits a 16/32-bit scalar access. Addresses on strict-alignment
// ISAs must be naturally aligned; the compiler legalizes arbitrary alignment.
// Loads zero-extend; stores truncate. A/B are dst/address or address/value.
func (e *ByteAssembler) NarrowMemory(i machine.Instruction) error {
	if !e.require((validInstructionRegisters(&i))) {
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
	code := uint32(0x79000000)
	if bits == 32 {
		code = 0xb9000000
	}
	if load {
		code |= 0x00400000
	}
	e.words = append(e.words, code|uint32(a)<<5|uint32(d))
	return nil
}

func (e *TextAssembler) NarrowMemory(i machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.NarrowMemory(i) }); err != nil {
		return err
	}
	if !i.ByteOrder.Valid() || i.ByteOrder == op.ByteOrderBigEndian {
		return machine.ErrUnsupportedOperation
	}
	switch i.Op {
	case machine.OpLoad16, machine.OpLoad32, machine.OpStore16, machine.OpStore32:
	default:
		return machine.ErrUnsupportedOperation
	}
	load := i.Op == machine.OpLoad16 || i.Op == machine.OpLoad32
	name := "strh"
	if load {
		name = "ldrh"
	}
	if i.Op == machine.OpLoad32 {
		name = "ldr"
	}
	if i.Op == machine.OpStore32 {
		name = "str"
	}
	d, a := i.A, i.B
	if !load {
		d, a = i.B, i.A
	}
	e.line("%s W%d, [X%d]", name, d, a)
	return nil
}
