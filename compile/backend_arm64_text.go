package compile

import (
	targetarm64 "github.com/moriyoshi/nyanjit/target/arm64"
)

var arm64FrameBytes = targetarm64.FrameBytes
var arm64FrameEnterSize = targetarm64.FrameEnterSize
var arm64FlagEffect = targetarm64.FlagEffect
var arm64PhysicalSize = targetarm64.PhysicalSize

type arm64TextAdapter struct{ *targetarm64.TextAssembler }

func (e *arm64TextAdapter) artifact() emissionArtifact { return emissionArtifact{assembly: e.Text()} }
