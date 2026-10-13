package amd64

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
	if !load && bits == 16 {
		e.code = append(e.code, 0x66)
	}
	rex := byte(0x40)
	if d >= 8 {
		rex |= 4
	}
	if a >= 8 {
		rex |= 1
	}
	if rex != 0x40 {
		e.code = append(e.code, rex)
	}
	opcode := byte(0x89)
	if load {
		opcode = 0x8b
		if bits == 16 {
			e.code = append(e.code, 0x0f)
			opcode = 0xb7
		}
	}
	e.code = append(e.code, opcode)
	base := byte(a & 7)
	mod := byte(0)
	if base == 5 {
		mod = 0x40
	}
	e.code = append(e.code, mod|byte(d&7)<<3|base)
	if base == 4 {
		e.code = append(e.code, 0x24)
	}
	if base == 5 {
		e.code = append(e.code, 0)
	}
	return nil
}

func (e *TextAssembler) NarrowMemory(i machine.Instruction) error {
	if err := e.validErr(func(enc *ByteAssembler) error { return enc.NarrowMemory(i) }); err != nil {
		return err
	}
	sizeBytes := narrowMemorySize(i)
	if sizeBytes == 0 {
		return machine.ErrUnsupportedOperation
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
		e.line(sizeBytes, "%s %s, %s ptr [%s]", mnemonic, amd64RegisterNames32[i.A], size, amd64Name(i.B))
	} else {
		value := amd64RegisterNames16[i.B]
		if bits == 32 {
			value = amd64RegisterNames32[i.B]
		}
		e.line(sizeBytes, "mov %s ptr [%s], %s", size, amd64Name(i.A), value)
	}
	return nil
}

func narrowMemorySize(i machine.Instruction) int {
	if !i.ByteOrder.Valid() || i.ByteOrder == op.ByteOrderBigEndian {
		return 0
	}
	switch i.Op {
	case machine.OpLoad16, machine.OpLoad32, machine.OpStore16, machine.OpStore32:
	default:
		return 0
	}
	address, data := i.A, i.B
	if i.Op == machine.OpLoad16 || i.Op == machine.OpLoad32 {
		address, data = i.B, i.A
	}
	size := MemorySize(address)
	if address < 8 && data < 8 {
		size--
	} // no REX is needed for 16/32-bit registers
	if i.Op == machine.OpLoad16 || i.Op == machine.OpStore16 {
		size++
	} // MOVZX opcode or operand-size prefix
	return size
}
