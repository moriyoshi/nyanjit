//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

// performanceWorkloads are the shapes every benchmark and the scaling gate
// measure.
func performanceWorkloads() []workload.Workload {
	return append(workload.All(), signAliasWorkload)
}

// performanceTarget returns the target and function settings for w. Workloads
// with outbound C calls run as foreign C entries.
func performanceTarget(w workload.Workload) (compile.TargetSpec, compile.Func) {
	var spec compile.TargetSpec
	if w.Scalar {
		spec.Vectors = compile.VectorsOff()
	}
	f := compile.Func{ABI: abi.C}
	if w.Foreign {
		f.Context = compile.ContextForeign
		f.Stack = compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}
	}
	return spec, f
}

// performanceVectorBits reports the preferred native SIMD width for w.
func performanceVectorBits(w workload.Workload) int {
	spec, _ := performanceTarget(w)
	target, err := compile.NewTarget(spec)
	if err != nil {
		return 0
	}
	return target.Vectors().PreferredBits
}

func preparePerformance(p ir.Input, w workload.Workload) error {
	spec, f := performanceTarget(w)
	_, err := lowerTemplate(p, spec, f)
	return err
}

// BenchmarkPrepare measures compilation alone, excluding IR construction and mapping.
// Work per source IR instruction is reported alongside Go's standard per-op metrics.
func BenchmarkPrepare(b *testing.B) {
	for _, w := range performanceWorkloads() {
		b.Run(w.Name, func(b *testing.B) {
			if w.SIMD && performanceVectorBits(w) == 0 {
				b.Skip("no native SIMD on this host")
			}
			p := w.Build(w.Size)
			instructions := float64(p.Len())
			b.ReportAllocs()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for b.Loop() {
				if err := preparePerformance(p, w); err != nil {
					b.Fatal(err)
				}
			}
			runtime.ReadMemStats(&after)
			total := float64(b.N) * instructions
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/total, "ns/insn")
			b.ReportMetric(float64(after.TotalAlloc-before.TotalAlloc)/total, "B/insn")
			b.ReportMetric(float64(after.Mallocs-before.Mallocs)/total, "allocs/insn")
			b.ReportMetric(instructions, "insns/op")
		})
	}
}

func TestPerformanceWorkloads(t *testing.T) {
	for _, w := range performanceWorkloads() {
		t.Run(w.Name, func(t *testing.T) {
			p := w.Build(min(w.Size, 100))
			if err := preparePerformance(p, w); err != nil {
				t.Fatal(fmt.Errorf("prepare %s: %w", w.Name, err))
			}
		})
	}
}
