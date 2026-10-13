package builder_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/instrument/irconform"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

// buildBothWays runs build once against recording Builders and once against
// segment Builders, and reports whether the sealed programs differ. It
// returns false when build made no Builder (a raw-IR program).
func buildBothWays(t *testing.T, name string, build func() (*ir.SealedProgram, error)) bool {
	t.Helper()
	want, wantErr := build()
	builders := 0
	restore := builder.SetSegmentMode(func(*builder.SegmentRecorder) { builders++ })
	got, gotErr := build()
	restore()
	if builders == 0 {
		return false
	}
	if (wantErr == nil) != (gotErr == nil) {
		t.Errorf("%s: Finish error %v, segment error %v", name, wantErr, gotErr)
		return true
	}
	if wantErr != nil {
		return true
	}
	if diff := builder.ProgramDifference(want.Clone(), got.Clone()); diff != "" {
		t.Errorf("%s: segment program differs: %s", name, diff)
	}
	return true
}

// TestSegmentCorpusEquivalence builds every workload, at several sizes, and
// every Builder-made conformance case both ways. The concatenated segments
// must equal Finish's program.
func TestSegmentCorpusEquivalence(t *testing.T) {
	programs := 0
	for _, w := range workload.All() {
		sizes := []int{1, 2, 3, 7, 16, 33}
		if w.ScalingSize == 0 {
			sizes = []int{1}
		}
		for _, n := range sizes {
			if strings.HasPrefix(w.Name, "pressure") && n < 4 {
				continue
			}
			build := func() (p *ir.SealedProgram, err error) {
				return w.Build(n), nil
			}
			if buildBothWays(t, w.Name, build) {
				programs++
			}
		}
	}
	for _, c := range irconform.Cases() {
		if buildBothWays(t, c.Name, c.Build) {
			programs++
		}
	}
	t.Logf("%d programs built both ways", programs)
}
