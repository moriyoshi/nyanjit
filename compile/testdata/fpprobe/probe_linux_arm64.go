package fpprobe

// Probe captures the caller's saved frame pointer around a generated call.
func Probe(address, argument uintptr) (before, result, after uintptr)

// FramePointer returns its caller's frame pointer, the head of the chain of
// saved frame records that frame-pointer unwinders follow.
func FramePointer() uintptr

// Load reads the word at address, a frame record the caller found by
// following frame pointers.
func Load(address uintptr) uintptr
