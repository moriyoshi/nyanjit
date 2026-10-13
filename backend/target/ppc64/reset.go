package ppc64

// Reset discards the assembled code and relocations so the assembler can
// assemble another function. The assembler has no other state; the code
// buffers are kept for reuse.
func (e *ByteAssembler) Reset() {
	e.words = e.words[:0]
	clear(e.relocations)
	e.relocations = e.relocations[:0]
}
