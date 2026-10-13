package compile

import "github.com/moriyoshi/nyanjit/backend/goregister"

// goUnwindInfo is the Go unwind table of a native managed frame. Its SP
// points come from final physical PCs, after branch layout.
type goUnwindInfo struct {
	CodeBytes  int
	FrameBytes int
	SP         []goregister.SPPoint
}
