package main

import (
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// BenchmarkPhase splits baseline compile into setup, build (Builder calls,
// including Segment hand-off), Lower and Template. phases.sh attributes a CPU
// profile of the /all benchmarks to the same phases.
func BenchmarkPhase(b *testing.B) {
	target, err := compile.NewTarget(compile.TargetSpec{Tier: compile.TierBaseline})
	if err != nil {
		b.Fatal(err)
	}
	f := compile.Func{ABI: abi.C}
	for _, k := range kernels {
		if k.name == "chain100" || k.name == "chain1000" {
			continue
		}
		b.Run(k.name+"/all", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				bb := nyanjit.NewBuilderFor(target, f)
				k.build(bb)
				u, err := bb.Lower()
				if err != nil {
					b.Fatal(err)
				}
				if _, err := u.Template(); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(k.name+"/setup", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = nyanjit.NewBuilderFor(target, f)
			}
		})
		b.Run(k.name+"/setup+build", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				bb := nyanjit.NewBuilderFor(target, f)
				k.build(bb)
			}
		})
		b.Run(k.name+"/setup+build+lower", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				bb := nyanjit.NewBuilderFor(target, f)
				k.build(bb)
				if _, err := bb.Lower(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
