package compile_test

import (
	"bytes"
	"errors"
	"reflect"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/irconform"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

func sealedView(p *ir.SealedProgram) *ir.Program {
	return &ir.Program{
		ParameterTypes: p.ParameterTypes(), Variables: p.Variables(), Instructions: p.Instructions(),
		ScopeExits: p.ScopeExits(), Control: p.Control(), Arity: p.Arity(), Result: p.Result(),
		ResultParts: p.ResultParts(),
	}
}

// streamUnit lowers p through a Stream fed with p's segments.
func streamUnit(t compile.Target, p *ir.SealedProgram, f compile.Func) (*compile.Unit, error) {
	s, err := compile.NewStream(t, f)
	if err != nil {
		return nil, err
	}
	if err := compile.ProgramSegments(sealedView(p), s.Segment); err != nil {
		return nil, err
	}
	return s.Unit()
}

type streamCase struct {
	name    string
	program *ir.SealedProgram
	f       compile.Func
}

func streamCorpus(t *testing.T) []streamCase {
	var cases []streamCase
	for _, w := range workload.All() {
		f := compile.Func{ABI: abi.C}
		if w.Foreign {
			f = compile.Func{ABI: abi.C, Context: compile.ContextForeign, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 20}}
		}
		size := w.Size
		if testing.Short() && w.ScalingSize != 0 {
			size = w.ScalingSize
		}
		cases = append(cases, streamCase{"workload/" + w.Name, w.Build(size), f})
	}
	for _, c := range irconform.Cases() {
		p, err := c.Build()
		if err != nil {
			continue
		}
		cases = append(cases, streamCase{c.Name, p, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}}})
	}
	return cases
}

// TestStreamOptimizedMatchesLower checks that a TierOptimized Stream fed
// with a program's segments lowers it exactly as Lower does.
func TestStreamOptimizedMatchesLower(t *testing.T) {
	cases := streamCorpus(t)
	for _, arch := range []string{"arm64", "amd64"} {
		target, err := compile.NewTarget(compile.TargetSpec{Architecture: arch})
		if err != nil {
			t.Fatal(err)
		}
		same, failed := 0, 0
		for _, c := range cases {
			want, werr := compile.Lower(target, c.program, c.f)
			got, gerr := streamUnit(target, c.program, c.f)
			if (werr == nil) != (gerr == nil) {
				t.Errorf("%s/%s: Lower error %v, Stream error %v", arch, c.name, werr, gerr)
				continue
			}
			if werr != nil {
				if !errors.Is(gerr, werr) && werr.Error() != gerr.Error() {
					t.Errorf("%s/%s: Lower error %v, Stream error %v", arch, c.name, werr, gerr)
				}
				failed++
				continue
			}
			wl, werr := want.Listing()
			gl, gerr := got.Listing()
			if wl != gl || (werr == nil) != (gerr == nil) {
				t.Errorf("%s/%s: listings differ (errors %v, %v)", arch, c.name, werr, gerr)
				continue
			}
			if !reflect.DeepEqual(want.Report().Snapshot(), got.Report().Snapshot()) {
				t.Errorf("%s/%s: reports differ", arch, c.name)
				continue
			}
			if arch == runtime.GOARCH {
				wt, werr := want.Template()
				gt, gerr := got.Template()
				if werr == nil && gerr == nil {
					wb, _ := wt.Bytes(nil)
					gb, _ := gt.Bytes(nil)
					if !bytes.Equal(wb, gb) {
						t.Errorf("%s/%s: template bytes differ", arch, c.name)
						continue
					}
				} else if !errors.Is(gerr, werr) && (werr == nil) != (gerr == nil) {
					t.Errorf("%s/%s: template errors %v, %v", arch, c.name, werr, gerr)
					continue
				}
			}
			same++
		}
		t.Logf("%s: %d of %d programs identical, %d rejected identically", arch, same, len(cases), failed)
	}
}
