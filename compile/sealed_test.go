package compile

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// checkSealedLoweringBorrowsFactsWithoutCloning runs for
// TestSealedLoweringBorrowsFactsWithoutCloning in package compile_test, which
// builds p with workload.Loops(80).
func checkSealedLoweringBorrowsFactsWithoutCloning(t *testing.T, p *ir.SealedProgram) {
	t.Helper()
	// Promoting Vars rewrites the program; this checks the path that does
	// not rewrite it.
	withoutVarPromotion(t)
	source, facts, err := validatedSource(p)
	if err != nil {
		t.Fatal(err)
	}
	source2, facts2, err := validatedSource(p)
	if err != nil || !sameIRStorage(source, source2) || facts != facts2 {
		t.Fatal("sealed source analyses were rebuilt")
	}
	u, err := lowerUnit(p, TargetSpec{Architecture: "amd64"}, Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Listing(); err != nil {
		t.Fatal(err)
	}
	if !sameIRStorage(u.input.source, source) || u.input.facts != facts {
		t.Fatal("lowering copied sealed storage or analyses")
	}
	raw := p.Clone()
	if raw == source {
		t.Fatal("explicit Clone did not detach")
	}
	a, af, err := validatedSource(raw)
	if err != nil {
		t.Fatal(err)
	}
	b, bf, err := validatedSource(raw)
	if err != nil || a == raw || b == raw || a == b || af == bf {
		t.Fatal("mutable adapter reused stale trusted storage")
	}
	raw.Instructions[0].Op = ir.Opcode(255)
	if _, err := lowerUnit(raw, TargetSpec{Architecture: "amd64"}, Func{}); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("raw edit was not revalidated: %v", err)
	}
	if _, err := u.Listing(); err != nil {
		t.Fatal(err)
	}
}

// checkSealedConcurrentLoweringsKeepFacts runs for
// TestSealedConcurrentLoweringsKeepFactsImmutable in package compile_test,
// which builds p with workload.Loops(80).
func checkSealedConcurrentLoweringsKeepFacts(t *testing.T, p *ir.SealedProgram) {
	t.Helper()
	source, facts, err := validatedSource(p)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(struct {
		Source *ir.Program
		Facts  *programFacts
	}{source, facts})
	type option struct {
		spec TargetSpec
		f    Func
	}
	platform := Func{Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}}
	options := []option{{TargetSpec{Architecture: "amd64"}, Func{}}, {TargetSpec{Architecture: "386"}, platform}, {TargetSpec{Architecture: "arm64"}, Func{}}, {TargetSpec{Architecture: "ppc64le"}, platform}}
	expected := make([]string, len(options))
	for n, o := range options {
		u, err := lowerUnit(p, o.spec, o.f)
		if err != nil {
			t.Fatal(err)
		}
		expected[n], err = u.Listing()
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			index := n % len(options)
			u, err := lowerUnit(p, options[index].spec, options[index].f)
			if err != nil {
				t.Error(err)
				return
			}
			got, err := u.Listing()
			if err != nil || got != expected[index] {
				t.Errorf("shared facts changed output for %s: %v", options[index].spec.Architecture, err)
			}
		}(n)
	}
	wg.Wait()
	after, _ := json.Marshal(struct {
		Source *ir.Program
		Facts  *programFacts
	}{source, facts})
	if !bytes.Equal(before, after) {
		t.Fatal("lowering mutated sealed IR or cached source facts")
	}
}

func TestBindingConstrainedImmediateStillValidates(t *testing.T) {
	raw := &ir.Program{Result: 1, Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 32, Imm: 1}, {Op: ir.OpShiftLeft, Bits: 32, A: 0, Imm: 1, Symbol: "count"}}}
	if _, err := ir.Seal(raw); !errors.Is(err, ir.ErrInvalidProgram) {
		t.Fatalf("non-constant binding accepted: %v", err)
	}
	if _, err := lowerListing(raw, TargetSpec{Architecture: "amd64"}, Func{Constants: map[string]uint64{"count": 32}}); !errors.Is(err, ErrInvalidFunction) {
		t.Fatalf("invalid bound shift accepted: %v", err)
	}
}

// TestConstantsKeepValidationErrors checks that Func.Constants does not hide
// the instruction a validation error names.
func TestConstantsKeepValidationErrors(t *testing.T) {
	raw := &ir.Program{Result: 2, Instructions: []ir.Instruction{{Op: ir.OpConst, Symbol: "k"}, {Op: ir.OpConst, Imm: 1}, {Op: ir.OpAdd, A: 0, B: 5}}}
	for _, constants := range []map[string]uint64{nil, {"k": 7}} {
		_, err := lowerListing(raw, TargetSpec{Architecture: "amd64"}, Func{Constants: constants})
		var invalid *ir.ValidationError
		if !errors.Is(err, ErrInvalidFunction) || !errors.Is(err, ir.ErrInvalidProgram) || !errors.As(err, &invalid) || invalid.Index != 2 {
			t.Fatalf("constants %v: %v, want a ValidationError at instruction 2", constants, err)
		}
	}
}

func TestNilSealedProgramRejected(t *testing.T) {
	var p *ir.SealedProgram
	for _, input := range []ir.Input{nil, p, &ir.SealedProgram{}} {
		if _, err := lowerUnit(input, TargetSpec{Architecture: "amd64"}, Func{}); !errors.Is(err, ErrInvalidFunction) {
			t.Fatalf("empty sealed input: %v", err)
		}
	}
}

func TestEmitterAcceptsSealedFragmentWithoutCloning(t *testing.T) {
	raw := &ir.Program{Result: 0, Instructions: []ir.Instruction{{Op: ir.OpConst, Bits: 32, Imm: 9}}}
	p, err := ir.Seal(raw)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := validatedSource(p)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEmitter()
	if err := e.AppendProgram(p); err != nil {
		t.Fatal(err)
	}
	if err := e.Exit(); err != nil {
		t.Fatal(err)
	}
	amd64Target, err := NewTarget(TargetSpec{Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.EmitAssembly(amd64Target, Func{}); err != nil {
		t.Fatal(err)
	}
	if !sameIRStorage(e.steps[0].fragment, source) {
		t.Fatal("emitter cloned immutable fragment")
	}
}

// sameIRStorage reports whether two program views borrow the same sealed
// instruction storage.
func sameIRStorage(a, b *ir.Program) bool {
	return a != nil && b != nil && len(a.Instructions) == len(b.Instructions) &&
		(len(a.Instructions) == 0 || &a.Instructions[0] == &b.Instructions[0])
}
