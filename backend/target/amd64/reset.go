package amd64

// Reset discards the assembled code, relocations, PC and error so the
// encoder can assemble another function. The stack practice, Go home count
// and CPU profile stay as constructed, and the code buffers are kept for
// reuse; Bytes and Relocations results taken before Reset are invalid after
// the next mutation.
func (e *ByteAssembler) Reset() {
	e.code = e.code[:0]
	e.relocations = e.relocations[:0]
	e.pc = 0
	e.err = nil
}
