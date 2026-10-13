package arm64

// Reset discards the assembled code, relocations, PC and error so the
// encoder can assemble another function. The stack practice and Go home
// count stay as constructed, and the code buffers are kept for reuse.
func (e *ByteAssembler) Reset() {
	e.words = e.words[:0]
	e.relocations = e.relocations[:0]
	e.pc = 0
	e.err = nil
}
