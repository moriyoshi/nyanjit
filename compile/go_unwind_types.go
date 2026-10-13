package compile

import "github.com/moriyoshi/nyanjit/goregister"

// goSPPoint records the SP delta effective starting at PC. These points
// come from final physical PCs, after the emitter has laid out branches.
type goSPPoint = goregister.SPPoint

type goUnwindInfo struct {
	CodeBytes  int
	FrameBytes int
	SP         []goSPPoint
	GoCalls    []int // return PCs, immediately after the call instruction
}
