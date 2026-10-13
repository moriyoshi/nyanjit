package compile

import (
	targetamd64 "github.com/moriyoshi/nyanjit/backend/target/amd64"
)

var amd64FrameBytes = targetamd64.FrameBytes
var amd64FlagEffect = targetamd64.FlagEffect
var amd64PhysicalSize = targetamd64.PhysicalSize

type amd64TextAdapter struct{ *targetamd64.TextAssembler }

func (e *amd64TextAdapter) artifact() emissionArtifact { return emissionArtifact{assembly: e.Text()} }
