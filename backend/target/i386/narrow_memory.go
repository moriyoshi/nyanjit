package i386

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// NarrowMemory emits a 16/32-bit scalar access. Addresses on strict-alignment
// ISAs must be naturally aligned; the compiler legalizes arbitrary alignment.
// Loads zero-extend; stores truncate. A/B are dst/address or address/value.
func (e *ByteAssembler) NarrowMemory(i machine.Instruction) error {
	if !e.require(validInstructionRegisters(i)) {
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
	opcode := byte(0x89)
	if load {
		opcode = 0x8b
		if bits == 16 {
			e.code = append(e.code, 0x0f)
			opcode = 0xb7
		}
	} else if bits == 16 {
		e.code = append(e.code, 0x66)
	}
	e.code = appendBase(e.code, opcode, d, a, 0)
	return nil
}

func (e *TextAssembler) NarrowMemory(i machine.Instruction) error {
	if !e.require(validInstructionRegisters(i)) {
		return e.err
	}

	if err := e.ByteAssembler.NarrowMemory(i); err != nil {
		return err
	}
	load := i.Op == machine.OpLoad16 || i.Op == machine.OpLoad32
	bits := 16
	if i.Op == machine.OpLoad32 || i.Op == machine.OpStore32 {
		bits = 32
	}
	size := "word"
	if bits == 32 {
		size = "dword"
	}
	if load {
		mnemonic := "movzx"
		if bits == 32 {
			mnemonic = "mov"
		}
		e.record("%s %s, %s ptr [%s]", mnemonic, name(i.A), size, name(i.B))
	} else {
		value := name(i.B)
		if bits == 16 {
			value = []string{"AX", "CX", "DX", "BX", "SP", "BP", "SI", "DI"}[i.B]
		}
		e.record("mov %s ptr [%s], %s", size, name(i.A), value)
	}
	return nil
}
