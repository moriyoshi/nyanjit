// Package goregister registers pointer-free generated functions with the
// Go runtime. The current implementation supports Go 1.26.x and 1.27.x on
// Linux/386, amd64, arm64, riscv64, loong64, and ppc64le with separate mirrors of each
// runtime layout. The nyanjit_no_goregister build tag disables registration and
// lookup without referencing private runtime linknames.
package goregister

import "errors"

var (
	ErrUnsupported       = errors.New("goregister: unsupported Go runtime or platform")
	ErrInvalidFrame      = errors.New("goregister: invalid generated frame")
	ErrRegistrationLimit = errors.New("goregister: process registration budget exhausted")
)

// MaxRegisteredFunctions caps permanent runtime modules and executable mappings.
// Reuse prepared templates and bound Code objects instead of binding per request.
const MaxRegisteredFunctions = 4096

// SPPoint gives the stack pointer delta effective starting at PC, measured
// from the function entry in bytes. PCs and deltas describe finalized code.
type SPPoint struct {
	PC    int
	Delta int
}

// Frame describes one pointer-free generated function covering all of code.
// The runtime receives empty argument and local pointer maps. CodeBytes must
// equal len(code), and FrameBytes is the largest stack pointer delta. SP must
// begin at PC zero and give each change in stack pointer depth.
type Frame struct {
	// Name is the function name shown in Go stack traces. An empty name uses
	// "goregister.generated".
	Name       string
	CodeBytes  int
	FrameBytes int
	ArgBytes   int // input and result stack bytes for a 386 Go ABI entry
	SP         []SPPoint
	// PointerFree asserts that no stack-resident Go address is live in this
	// frame. Heap targets must stay live across every callback; raw uintptr
	// values do not supply GC roots. Stack addresses must never enter it.
	PointerFree bool
}
