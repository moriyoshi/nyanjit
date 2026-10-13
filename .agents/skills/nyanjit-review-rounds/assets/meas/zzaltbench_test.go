package compile_test

import (
	"strconv"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

// zzAltPrepare is the old compile.Prepare: resolve the host target, lower and
// encode a template, as the repository's BenchmarkPrepareAlternating does.
func zzAltPrepare(p ir.Input, f compile.Func) (*execution.Template, error) {
	t, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		return nil, err
	}
	u, err := compile.Lower(t, p, f)
	if err != nil {
		return nil, err
	}
	return u.Template()
}

// Copy of round 8's BenchmarkPrepareAlternating (public API) with an extra 1:10 gap, so r6/r7/post run identical code.
func BenchmarkZZPrepareAlternating(b *testing.B) {
	f := compile.Func{ABI: abi.C, Context: compile.ContextNative, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 24}}
	tiny := workload.Tiny(0)
	for _, w := range []struct {
		name    string
		program *ir.SealedProgram
	}{{"scopes10000", workload.Scopes(10000)}, {"chain8000", workload.Chain(8000)}, {"scopes800", workload.Scopes(800)}} {
		for _, gap := range []int{1, 2, 3, 10} {
			b.Run(w.name+"/1:"+strconv.Itoa(gap), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := zzAltPrepare(w.program, f); err != nil {
						b.Fatal(err)
					}
					for i := 0; i < gap; i++ {
						if _, err := zzAltPrepare(tiny, f); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}
