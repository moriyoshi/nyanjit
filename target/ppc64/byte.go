package ppc64

import (
	"encoding/binary"

	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

// ByteAssembler renders concrete instructions in the selected instruction order.
type ByteAssembler struct {
	order       ir.ByteOrder
	words       []uint32
	relocations []machine.Relocation
}

func NewByteAssembler(order ir.ByteOrder) (*ByteAssembler, error) {
	if order != ir.ByteOrderBigEndian && order != ir.ByteOrderLittleEndian {
		return nil, machine.ErrUnsupportedOperation
	}
	return &ByteAssembler{order: order}, nil
}

func (e *ByteAssembler) Visit(ins Instruction) error {
	word, err := Encode(ins)
	if err != nil {
		return err
	}
	if ins.Symbol != "" {
		if ins.Op != Addis {
			return invalidInstruction(ins)
		}
		encoding := machine.RelocPPC64BigEndian
		if e.order == ir.ByteOrderLittleEndian {
			encoding = machine.RelocPPC64LittleEndian
		}
		e.relocations = append(e.relocations, machine.Relocation{Symbol: ins.Symbol, Offset: e.AssembledSize(), Encoding: encoding})
	}
	e.words = append(e.words, word)
	return nil
}

func (e *ByteAssembler) AssembledSize() int { return len(e.words) * 4 }
func (e *ByteAssembler) Relocations() []machine.Relocation {
	return append([]machine.Relocation(nil), e.relocations...)
}
func (e *ByteAssembler) Bytes() []byte {
	var order binary.ByteOrder = binary.BigEndian
	if e.order == ir.ByteOrderLittleEndian {
		order = binary.LittleEndian
	}
	code := make([]byte, e.AssembledSize())
	for i, word := range e.words {
		order.PutUint32(code[i*4:], word)
	}
	return code
}

func (e *ByteAssembler) Data(data []byte) error {
	if len(data)%4 != 0 {
		return machine.ErrUnsupportedOperation
	}
	var order binary.ByteOrder = binary.BigEndian
	if e.order == ir.ByteOrderLittleEndian {
		order = binary.LittleEndian
	}
	for i := 0; i < len(data); i += 4 {
		e.words = append(e.words, order.Uint32(data[i:]))
	}
	return nil
}
