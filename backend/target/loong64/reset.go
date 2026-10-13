package loong64

// Reset discards the assembled code, relocations, PC, pending comparison and
// error so the encoder can assemble another function. The stack practice and
// Go home count stay as constructed, and the code buffers are kept for reuse.
func (e *ByteAssembler) Reset() {
	clear(e.plan)
	e.plan = e.plan[:0]
	clear(e.relocations)
	e.relocations = e.relocations[:0]
	e.pc = 0
	e.left, e.right = 0, 0
	e.err = nil
}
