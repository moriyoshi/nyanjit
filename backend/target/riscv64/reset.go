package riscv64

// Reset discards the assembled code, literal marks, relocations, PC, error
// and recorded compare operands so the encoder can assemble another
// function. The stack practice, Go home count and CPU profile stay as
// constructed, and the code buffers are kept for reuse.
func (e *ByteAssembler) Reset() {
	e.words = e.words[:0]
	clear(e.literalData)
	e.relocations = e.relocations[:0]
	e.pc = 0
	e.err = nil
	e.compareLeft, e.compareRight = 0, 0
}
