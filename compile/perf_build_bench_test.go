//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"runtime"
	"testing"
)

// BenchmarkBuildScopes includes validation/sealing and cached source analysis,
// which Prepare benchmarks deliberately exclude.
func BenchmarkBuildScopes(b *testing.B) {
	for _, w := range performanceWorkloads() {
		switch w.Name {
		case "many-vars", "many-vars-loop", "deep-scopes", "deep-scope-loops", "nested-loops":
		default:
			continue
		}
		b.Run(w.Name, func(b *testing.B) {
			count := float64(w.Build(w.Size).Len())
			b.ReportAllocs()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for b.Loop() {
				p := w.Build(w.Size)
				runtime.KeepAlive(p)
			}
			runtime.ReadMemStats(&after)
			total := float64(b.N) * count
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/total, "ns/insn")
			b.ReportMetric(float64(after.TotalAlloc-before.TotalAlloc)/total, "B/insn")
			b.ReportMetric(float64(after.Mallocs-before.Mallocs)/total, "allocs/insn")
			b.ReportMetric(count, "insns/op")
		})
	}
}

// BenchmarkBuild includes construction, validation, sealing and source facts.
func BenchmarkBuild(b *testing.B) {
	for _, w := range performanceWorkloads() {
		switch w.Name {
		case "tiny", "small", "chain", "scopes", "loops":
		default:
			continue
		}
		b.Run(w.Name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				runtime.KeepAlive(w.Build(w.Size))
			}
		})
	}
}
