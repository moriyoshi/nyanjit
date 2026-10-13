package arm64

import "github.com/moriyoshi/nyanjit/backend/machine"

// FuseLoops implements machine.LoopFuser with machine.FuseZeroLoops.
func (Policy) FuseLoops(s *machine.Stream, labels []int, bound []bool) ([]int, []bool, error) {
	labels, err := machine.FuseZeroLoops(s, labels, bound, "arm64")
	return labels, bound, err
}
