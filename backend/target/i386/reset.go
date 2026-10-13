package i386

// Reset discards the assembled code, relocations, PC and first error, and the
// frame and call area that FrameEnterArea installed, so the encoder can
// assemble another function. The stack practice stays as constructed, and
// the code buffers are kept for reuse: code returned by Bytes before Reset is
// overwritten by later encoding.
func (e *ByteAssembler) Reset() {
	e.code = e.code[:0]
	e.relocations = e.relocations[:0]
	e.pc = 0
	e.err = nil
	e.frame, e.callArea = 0, 0
}
