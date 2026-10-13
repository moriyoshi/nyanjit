package ppc64

import (
	"encoding/binary"

	"github.com/moriyoshi/nyanjit/backend/machine"
)

// ByteAssembler renders concrete instructions as little-endian words.
type ByteAssembler struct {
	words       []uint32
	relocations []machine.Relocation
}

func NewByteAssembler() *ByteAssembler { return &ByteAssembler{} }

func (e *ByteAssembler) Visit(ins Instruction) error {
	word, err := Encode(ins)
	if err != nil {
		return err
	}
	if ins.Symbol != "" {
		if ins.Op != Addis {
			return invalidInstruction(ins)
		}
		e.relocations = append(e.relocations, machine.Relocation{Symbol: ins.Symbol, Offset: e.AssembledSize(), Encoding: machine.RelocPPC64LittleEndian})
	}
	e.words = append(e.words, word)
	return nil
}

func (e *ByteAssembler) AssembledSize() int { return len(e.words) * 4 }

// Relocations returns read-only borrowed entries valid until the next assembler
// mutation. Copy the slice to retain or modify it.
func (e *ByteAssembler) Relocations() []machine.Relocation {
	return e.relocations
}

// Bytes returns encoded bytes, valid until the next assembler mutation.
// Treat the result as read-only; copy it to retain or modify it.
func (e *ByteAssembler) Bytes() []byte {
	code := make([]byte, e.AssembledSize())
	for i, word := range e.words {
		binary.LittleEndian.PutUint32(code[i*4:], word)
	}
	return code
}

func (e *ByteAssembler) Data(data []byte) error {
	if len(data)%4 != 0 {
		return machine.ErrUnsupportedOperation
	}
	for i := 0; i < len(data); i += 4 {
		e.words = append(e.words, binary.LittleEndian.Uint32(data[i:]))
	}
	return nil
}
