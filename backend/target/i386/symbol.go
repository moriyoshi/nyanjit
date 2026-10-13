package i386

import "github.com/moriyoshi/nyanjit/backend/machine"

func (e *ByteAssembler) SymbolInstruction(i machine.Instruction) {
	if !e.require(validInstructionRegisters(i)) {
		return
	}

	if i.Bits != 64 {
		e.Symbol(i.A, i.Symbol)
		return
	}
	e.relocations = append(e.relocations, machine.Relocation{Symbol: i.Symbol, Offset: len(e.code), Encoding: machine.Reloc386WideImmediate})
	e.Constant(i.A, 0)
	e.Constant(i.HighA, 0)
}
func (e *TextAssembler) SymbolInstruction(i machine.Instruction) {
	if !e.require(validInstructionRegisters(i)) {
		return
	}

	if i.Bits != 64 {
		e.Symbol(i.A, i.Symbol)
		return
	}
	e.record("mov %s, low32(%s)", name(i.A), i.Symbol)
	e.record("mov %s, high32(%s)", name(i.HighA), i.Symbol)
	e.ByteAssembler.SymbolInstruction(i)
}
