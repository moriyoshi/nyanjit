// Package workload builds deterministic programs for measuring nyanjit's
// compile time. nyanjit's benchmarks and its linear-scaling gate use these
// shapes, and users can use them for reproducible performance reports.
//
// Every generator returns the same program for the same n, panics only on an
// internal error, and returns a new SealedProgram on every call. Programs are
// for compiling, not running: Calls targets a fixed fake address, and the
// vector shapes read and write through their pointer arguments.
package workload

import (
	"runtime"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// Workload is one named program shape and the settings nyanjit's benchmarks
// compile it with.
type Workload struct {
	// Name identifies the shape in benchmark and gate output.
	Name string
	// Build returns the program for size n.
	Build func(n int) *ir.SealedProgram
	// Size is the default benchmark size. Fixed-size shapes have size 1.
	Size int
	// ScalingSize is the middle size for the linear-scaling gate, which
	// compiles sizes n/2 through 4n. It is zero for fixed-size shapes.
	ScalingSize int
	// Foreign means the program calls C functions, so it compiles as a
	// foreign C entry (compile.ContextForeign) with a stack bound.
	Foreign bool
	// Scalar compiles the program with compile.VectorsOff to measure the
	// scalar fallback of its vector operations.
	Scalar bool
	// SIMD measures native vector code: skip the workload on a target
	// without SIMD.
	SIMD bool
}

// All returns every workload, sized for the host architecture: on 386, the
// Var-heavy and outer-value shapes are half size so their 64-bit values fit
// the spill budget.
// The result is a new slice; callers may modify it.
func All() []Workload {
	vars := 1
	if runtime.GOARCH == "386" {
		vars = 2
	}
	return []Workload{
		{Name: "tiny", Build: Tiny, Size: 1},
		{Name: "small", Build: Small, Size: 1},
		{Name: "chain", Build: Chain, Size: 8000, ScalingSize: 2000},
		{Name: "pressure", Build: Pressure, Size: 8000, ScalingSize: 500},
		{Name: "fp", Build: FP, Size: 8000, ScalingSize: 500},
		{Name: "loops", Build: Loops, Size: 8000, ScalingSize: 1000},
		{Name: "calls", Build: Calls, Size: 2000, ScalingSize: 200, Foreign: true},
		{Name: "vectors", Build: Vectors, Size: 3000, ScalingSize: 400},
		{Name: "scopes", Build: Scopes, Size: 800, ScalingSize: 400},
		{Name: "many-vars", Build: ManyVars, Size: 2000 / vars, ScalingSize: 500 / vars},
		{Name: "many-vars-loop", Build: ManyVarsLoop, Size: 2000 / vars, ScalingSize: 500 / vars},
		{Name: "deep-scopes", Build: DeepScopes, Size: 2000 / vars, ScalingSize: 500 / vars},
		{Name: "deep-scope-loops", Build: DeepScopeLoops, Size: 800, ScalingSize: 400},
		{Name: "nested-loops", Build: NestedLoops, Size: 800, ScalingSize: 400},
		{Name: "outer-values", Build: OuterValues, Size: 400 / vars, ScalingSize: 200 / vars},
		{Name: "outer-values-threaded", Build: OuterValuesThreaded, Size: 400 / vars, ScalingSize: 200 / vars},
		{Name: "sha256-scalar", Build: SHA256, Size: 1, Scalar: true},
		{Name: "sha256-simd", Build: SHA256, Size: 1, SIMD: true},
	}
}

func finish(b *nyanjit.Builder) *ir.SealedProgram {
	p, err := b.Finish()
	if err != nil {
		panic(err)
	}
	return p
}
