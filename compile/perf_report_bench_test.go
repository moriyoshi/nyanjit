//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"runtime"
	"testing"
)

// BenchmarkAnalyze exposes the cost of requesting a report alone. It uses
// exactly the Prepare fixtures and excludes construction and executable mapping.
func BenchmarkAnalyze(b *testing.B) {
	for _, w := range performanceWorkloads() {
		b.Run(w.Name, func(b *testing.B) {
			if w.SIMD && performanceVectorBits(w) == 0 {
				b.Skip("no native SIMD")
			}
			p := w.Build(w.Size)
			spec, f := performanceTarget(w)
			instructions := float64(p.Len())
			b.ReportAllocs()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for b.Loop() {
				if _, err := lowerReport(p, spec, f); err != nil {
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
