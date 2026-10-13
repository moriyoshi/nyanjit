package i386

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
)

// Literal emits absolute SSE2 loads. The displacement is patched to the final
// code base plus the pool PC while the executable mapping is still writable.
func (e *ByteAssembler) Literal(i machine.Instruction) error {
	if i.Op == machine.OpLiteralData {
		e.code = append(e.code, i.Data...)
		return nil
	}
	if !backend.IsVectorRegister(i.A) || backend.VectorNumber(i.A) >= 8 {
		return machine.ErrUnsupportedOperation
	}
	prefix, op := byte(0xf2), byte(0x10)
	switch i.Bits {
	case 32:
		prefix = 0xf3
	case 64:
	case 128:
		prefix, op = 0xf3, 0x6f
	default:
		return machine.ErrUnsupportedOperation
	}
	e.relocations = append(e.relocations, machine.Relocation{Encoding: machine.Reloc386Literal, Offset: len(e.code), TargetOffset: i.Disp})
	e.code = append(e.code, prefix, 0x0f, op, 0x05|backend.VectorNumber(i.A)<<3, 0, 0, 0, 0)
	return nil
}
func (e *TextAssembler) Literal(i machine.Instruction) error {
	if i.Op == machine.OpLiteralData {
		e.record(".byte % x", i.Data)
		return e.ByteAssembler.Literal(i)
	}
	mnemonic := "movsd"
	if i.Bits == 32 {
		mnemonic = "movss"
	}
	if i.Bits == 128 {
		mnemonic = "movdqu"
	}
	// Text needs no process address: the symbolic pool PC retains the same
	// relocation and byte-size information as native assembly.
	e.record("%s xmm%d, [literal + %#x]", mnemonic, backend.VectorNumber(i.A), i.Disp)
	return e.ByteAssembler.Literal(i)
}
