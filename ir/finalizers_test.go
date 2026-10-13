package ir_test

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestFinalizersShared checks that Clone, Seal and SealedProgram share one
// Finalizers list, which execution counts Codes by.
func TestFinalizersShared(t *testing.T) {
	p := &ir.Program{Arity: 1, Result: 0, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: 64}},
		Finalizers: backend.Finalizers{backend.FinalizerFunc(func() {}), backend.FinalizerFunc(func() {})}}
	same := func(name string, fs backend.Finalizers) {
		t.Helper()
		if len(fs) != 2 || &fs[0] != &p.Finalizers[0] {
			t.Fatalf("%s does not share the list", name)
		}
	}
	same("Clone", p.Clone().Finalizers)
	sealed, err := ir.Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	same("Seal", sealed.Finalizers())
	same("SealedProgram.Clone", sealed.Clone().Finalizers)
	// Appending to a clone's list cannot write into the shared one.
	clone := p.Clone()
	clone.Finalizers = append(clone.Finalizers, nil)
	if &clone.Finalizers[0] == &p.Finalizers[0] {
		t.Fatal("append wrote into the shared list")
	}
}
